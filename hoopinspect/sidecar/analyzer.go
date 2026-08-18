package sidecar

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/hoophq/hoopinspect"
	"github.com/hoophq/hoopinspect/analyzer"

	"github.com/hoophq/hoopinspect/policy"
)

// HTTPCodecConfig controls what a lane's HTTP codec exposes to policy.
//
// It exists because the codec's capture options are a per-lane decision the
// registry cannot express: codec/http registers a factory taking no
// arguments, so every lane in the process shared one zero-value Options and
// no lane could see a request body. An AI analyzer on an HTTP lane needs the
// body ("POST /anything" with no body tells a model nothing), so the option
// had to become reachable from the config file.
//
// The defaults still expose nothing. Turning capture on is an explicit act,
// because everything captured reaches the policy engine, the audit trail and,
// where an analyzer is configured, a third party.
type HTTPCodecConfig struct {
	// CaptureBody includes request and response bodies in the Statement.
	CaptureBody bool `json:"capture_body"`

	// MaxBodyBytes truncates a captured body. Zero uses the codec default.
	MaxBodyBytes int `json:"max_body_bytes,omitempty"`

	// Headers names the headers to expose, matched case-insensitively.
	// There is no capture-all.
	Headers []string `json:"headers,omitempty"`
}

// forbiddenHeaders are never allowlistable.
//
// A lane that exposes Authorization to policy has put a bearer token into
// every decision log, every audit record and every prompt. The codec's own
// doc calls this out as the reason the allowlist exists; refusing the three
// names outright means an operator cannot reintroduce the problem by writing
// one line of YAML.
var forbiddenHeaders = []string{"authorization", "cookie", "proxy-authorization", "set-cookie"}

func (h *HTTPCodecConfig) validate(lane string) []string {
	if h == nil {
		return nil
	}
	var problems []string
	for _, name := range h.Headers {
		lower := strings.ToLower(strings.TrimSpace(name))
		for _, bad := range forbiddenHeaders {
			if lower == bad {
				problems = append(problems, fmt.Sprintf(
					"listener %q: header %q may not be exposed to policy", lane, name))
			}
		}
	}
	if h.MaxBodyBytes < 0 {
		problems = append(problems, fmt.Sprintf("listener %q: http.max_body_bytes is negative", lane))
	}
	return problems
}

// AnalyzerConfig configures the AI risk analyzer for the whole process.
//
// One provider serves every lane. Per-lane variation lives in the rules,
// which is where an operator already expresses per-lane policy; a second
// provider per lane would double the credential surface for a case nobody
// has asked for.
type AnalyzerConfig struct {
	// Provider names a registered provider: anthropic, openai, vertex.
	// Availability depends on what the binary links.
	Provider string `json:"provider"`

	// Model names the model. Provider-specific format.
	Model string `json:"model"`

	// Endpoint overrides the provider's default URL. Empty uses the
	// provider default.
	Endpoint string `json:"endpoint,omitempty"`

	// CredentialsFile is the path to the API key or service-account key.
	//
	// A PATH, never the material. The file must not be readable by group
	// or other, and the process reads it once at startup.
	//
	// Optional for a provider that resolves ambient credentials: under GKE
	// Workload Identity, Vertex needs no file at all, which is the
	// strongest form of this control because there is nothing on disk to
	// leak or rotate.
	CredentialsFile string `json:"credentials_file,omitempty"`

	// Extra carries provider-specific settings, such as Vertex's project
	// and region.
	Extra map[string]string `json:"extra,omitempty"`

	// Prompt replaces the built-in risk guidance for every ai_analysis rule
	// that does not set its own. Empty uses analyzer.PromptGuidance.
	//
	// PROCESS-WIDE, and that is the trap: it reaches the http lane as well
	// as the database one. Guidance written as "you are classifying SQL
	// against a customer database" leaves an HTTP lane's model reasoning
	// about DROP and TRUNCATE while it looks at a JSON body. Keep this
	// protocol-neutral and put protocol-specific wording on the rule, whose
	// Prompt wins.
	//
	// Neither can remove the output contract: the call-one-tool instruction
	// and the never-quote-a-literal rule are appended after whatever is set
	// here. The second is a security property, not a style preference.
	Prompt string `json:"prompt,omitempty"`

	// TimeoutSec bounds one classification. Zero uses the analyzer default.
	TimeoutSec int `json:"timeout_sec,omitempty"`

	// FailOpen allows a statement whose classification failed.
	//
	// It is a POINTER so an unset value can default to true, which is the
	// opposite of every other evaluator in this system and deliberate: a
	// classifier that denies whenever its provider has an outage takes the
	// database down with it. Set it false where the classification is a
	// compliance requirement.
	FailOpen *bool `json:"fail_open,omitempty"`

	// Send controls what leaves the process: raw, redacted or refuse.
	Send SendMode `json:"send,omitempty"`

	// MaxInputBytes bounds the content sent per statement.
	MaxInputBytes int `json:"max_input_bytes,omitempty"`

	// Cache configures the verdict cache.
	Cache AnalyzerCacheConfig `json:"cache,omitempty"`

	// MaxCalls bounds classifications for the process lifetime. Zero is
	// unbounded. A backstop against a pathological workload, not a quota.
	MaxCalls int `json:"max_calls,omitempty"`

	// MaxOutputTokens bounds the model's reply. Zero uses the provider
	// default.
	MaxOutputTokens int `json:"max_output_tokens,omitempty"`
}

// SendMode decides what a statement looks like when it leaves the process.
type SendMode string

const (
	// SendRaw transmits the statement as the client wrote it.
	SendRaw SendMode = "raw"

	// SendRedacted masks detected entities before transmitting.
	//
	// This is the mode a deployment running PII detection should choose. A
	// relay whose purpose is keeping taxpayer ids out of a database's query
	// log cannot then post those ids to a model vendor, and it has a
	// detector in-process already.
	SendRedacted SendMode = "redacted"

	// SendRefuse denies locally when a statement contains a detected
	// entity, rather than sending it anywhere.
	SendRefuse SendMode = "refuse"
)

// AnalyzerCacheConfig bounds the verdict cache.
type AnalyzerCacheConfig struct {
	// Size is the maximum number of cached verdicts. Zero disables.
	Size int `json:"size,omitempty"`

	// TTLSec expires an entry. Zero disables the cache, because an entry
	// that never expires outlives a prompt or model change.
	TTLSec int `json:"ttl_sec,omitempty"`
}

// failOpen resolves the pointer default.
func (a *AnalyzerConfig) failOpen() bool {
	if a == nil || a.FailOpen == nil {
		return true
	}
	return *a.FailOpen
}

// validate checks the analyzer section in isolation.
func (a *AnalyzerConfig) validate(hasScanner bool) []string {
	if a == nil {
		return nil
	}
	var problems []string

	if a.Provider == "" {
		problems = append(problems, "analyzer: no provider set")
	} else if !providerLinked(a.Provider) {
		problems = append(problems, fmt.Sprintf(
			"analyzer: provider %q is not linked into this binary (linked: %s)",
			a.Provider, strings.Join(analyzer.RegisteredProviders(), ", ")))
	}
	if a.Model == "" {
		problems = append(problems, "analyzer: no model set")
	}

	switch a.Send {
	case "", SendRaw:
	case SendRedacted, SendRefuse:
		if !hasScanner {
			// A mode that cannot do what it says is refused rather
			// than downgraded: "redacted" with no detector would
			// transmit raw text under a name that promises otherwise.
			problems = append(problems, fmt.Sprintf(
				"analyzer: send=%q needs a pii section to detect with", a.Send))
		}
	default:
		problems = append(problems, fmt.Sprintf(
			"analyzer: unknown send mode %q (raw, redacted or refuse)", a.Send))
	}

	if a.Endpoint != "" {
		if err := validateEndpoint(a.Endpoint); err != nil {
			problems = append(problems, "analyzer: "+err.Error())
		}
	}
	if a.TimeoutSec < 0 {
		problems = append(problems, "analyzer: timeout_sec is negative")
	}
	if a.MaxInputBytes < 0 {
		problems = append(problems, "analyzer: max_input_bytes is negative")
	}

	// Negative is refused rather than clamped. Every one of these is a cost
	// or safety bound whose zero value means "off", and a negative reads as
	// off too, so a typo silently removes the ceiling the operator wrote
	// down. max_calls is the sharp one: it is the last line against a
	// runaway workload, and -1 would disable it while looking set.
	if a.MaxCalls < 0 {
		problems = append(problems, "analyzer: max_calls is negative")
	}
	if a.MaxOutputTokens < 0 {
		problems = append(problems, "analyzer: max_output_tokens is negative")
	}
	if a.Cache.Size < 0 {
		problems = append(problems, "analyzer: cache.size is negative")
	}
	if a.Cache.TTLSec < 0 {
		problems = append(problems, "analyzer: cache.ttl_sec is negative")
	}
	return problems
}

// validateEndpoint refuses a URL that would carry a credential.
//
// GET /config reports the analyzer's endpoint so an operator can confirm what
// a lane talks to, and that view is served beside a read interface to the
// audit trail. A credential in userinfo or a query string would be published
// there. Refusing the shape is more durable than remembering to strip it.
func validateEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("endpoint is not a valid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("endpoint scheme %q is not http or https", u.Scheme)
	}
	if u.User != nil {
		return fmt.Errorf("endpoint carries credentials in its userinfo; put them in credentials_file")
	}
	if u.RawQuery != "" {
		return fmt.Errorf("endpoint carries a query string; a credential there would be published by /config")
	}
	return nil
}

func providerLinked(name string) bool {
	for _, got := range analyzer.RegisteredProviders() {
		if got == name {
			return true
		}
	}
	return false
}

// endpointHost renders the endpoint for /config: host only, never the path or
// anything that could carry a token.
func (a *AnalyzerConfig) endpointHost() string {
	if a == nil || a.Endpoint == "" {
		return ""
	}
	u, err := url.Parse(a.Endpoint)
	if err != nil {
		return ""
	}
	return u.Host
}

// buildAnalyzer constructs the shared provider from the analyzer section.
//
// It is called once per process, not per lane: one provider, one credential
// read, one token source.
func buildAnalyzer(cfg *AnalyzerConfig) (analyzer.Provider, error) {
	if cfg == nil {
		return nil, nil
	}

	var cred analyzer.Secret
	if cfg.CredentialsFile != "" {
		var err error
		cred, err = analyzer.ReadSecretFile(cfg.CredentialsFile)
		if err != nil {
			return nil, err
		}
	}

	return analyzer.NewProvider(cfg.Provider, analyzer.Options{
		Model:           cfg.Model,
		Endpoint:        cfg.Endpoint,
		Credential:      cred,
		Extra:           cfg.Extra,
		MaxOutputTokens: cfg.MaxOutputTokens,
	})
}

// splitAnalyzerRules separates ai_analysis rules from the local rule set.
//
// Rules cannot evaluate an ai_analysis rule and rejects one that reaches it,
// so the split has to happen before NewRules. Order is preserved within each
// group, which matters for the locals: first match still wins.
func splitAnalyzerRules(rules []policy.Rule) (local, ai []policy.Rule) {
	for _, r := range rules {
		if r.Type == policy.MatchAIAnalysis {
			ai = append(ai, r)
			continue
		}
		local = append(local, r)
	}
	return local, ai
}

// buildAnalyzerEvaluators turns ai_analysis rules into evaluators.
//
// Each rule becomes its own Evaluator, so two rules on one lane get their own
// trigger, action map and denial message while sharing the provider and,
// through the provider, the credential.
func buildAnalyzerEvaluators(
	rules []policy.Rule,
	cfg *AnalyzerConfig,
	provider analyzer.Provider,
	redact func(string) string,
) ([]policy.Evaluator, error) {
	if len(rules) == 0 {
		return nil, nil
	}
	if cfg == nil || provider == nil {
		return nil, fmt.Errorf(
			"ai_analysis rule %q needs an analyzer section, and none is configured", rules[0].Name)
	}

	out := make([]policy.Evaluator, 0, len(rules))
	for _, r := range rules {
		actions, err := actionMap(r)
		if err != nil {
			return nil, err
		}
		// Rule prompt beats the analyzer default beats the built-in.
		guidance := r.Prompt
		if guidance == "" {
			guidance = cfg.Prompt
		}
		ev, err := analyzer.New(analyzer.Config{
			Rule:          r.Name,
			Provider:      provider,
			Guidance:      guidance,
			Actions:       actions,
			Trigger:       triggerFrom(r.Trigger),
			Message:       r.Message,
			Timeout:       time.Duration(cfg.TimeoutSec) * time.Second,
			FailOpen:      cfg.failOpen(),
			MaxInputBytes: cfg.MaxInputBytes,
			CacheSize:     cfg.Cache.Size,
			CacheTTL:      time.Duration(cfg.Cache.TTLSec) * time.Second,
			MaxCalls:      cfg.MaxCalls,
			Redact:        redact,
		})
		if err != nil {
			return nil, fmt.Errorf("rule %q: %w", r.Name, err)
		}
		out = append(out, ev)
	}
	return out, nil
}

func triggerFrom(t *policy.AITrigger) analyzer.Trigger {
	if t == nil {
		return analyzer.Trigger{}
	}
	return analyzer.Trigger{
		Operations: t.Operations,
		Tables:     t.Tables,
		Resources:  t.Resources,
	}
}

func actionMap(r policy.Rule) (analyzer.ActionMap, error) {
	m := analyzer.ActionMap{}
	for level, raw := range map[analyzer.RiskLevel]string{
		analyzer.RiskHigh:   r.HighRisk,
		analyzer.RiskMedium: r.MediumRisk,
		analyzer.RiskLow:    r.LowRisk,
	} {
		if raw == "" {
			continue
		}
		a := analyzer.Action(raw)
		if !a.Valid() {
			return nil, fmt.Errorf("rule %q: unknown action %q for %s risk", r.Name, raw, level)
		}
		m[level] = a
	}
	return m, nil
}

// setupAnalyzer builds the process-wide analyzer from the config.
//
// Returns (nil, nil) when no analyzer section is present, which is the
// no-analyzer build: every lane then resolves as it did before this feature
// existed, and an ai_analysis rule fails later with a message naming the
// missing section.
func setupAnalyzer(cfg *Config, det Plugin) (*analyzerDeps, error) {
	if cfg == nil || cfg.Analyzer == nil {
		return nil, nil
	}
	if problems := cfg.Analyzer.validate(det != nil); len(problems) > 0 {
		return nil, fmt.Errorf("invalid config:\n  - %s", strings.Join(problems, "\n  - "))
	}

	provider, err := buildAnalyzer(cfg.Analyzer)
	if err != nil {
		return nil, err
	}
	return &analyzerDeps{
		cfg:      cfg.Analyzer,
		provider: provider,
		redact:   redactorFor(cfg.Analyzer.Send, det),
	}, nil
}

// verifier is the optional capability a provider implements when its
// credential can be proven without calling the model.
//
// Optional because only Vertex has something to prove: an API key is not
// verifiable short of spending a request, but minting a GCP token is free and
// catches the whole class of IAM, key and clock problems.
type verifier interface {
	Verify(ctx context.Context) error
}

// verifyAnalyzer proves the credential at startup where the provider can.
func verifyAnalyzer(ac *analyzerDeps) error {
	if ac == nil || ac.provider == nil {
		return nil
	}
	v, ok := ac.provider.(verifier)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return v.Verify(ctx)
}

// sendModeOrDefault renders the effective send mode for a log line.
func sendModeOrDefault(m SendMode) SendMode {
	if m == "" {
		return SendRaw
	}
	return m
}

// redactorFor builds the function that rewrites content before it leaves the
// process.
//
// This is the control that makes an in-process detector worth having twice
// over: a relay whose pitch is keeping taxpayer ids out of a database's query
// log must not post those ids to a model vendor. The detector is already
// here, so using it costs one scan.
func redactorFor(mode SendMode, det Plugin) func(string) string {
	if det == nil {
		return nil
	}
	switch mode {
	case SendRedacted:
		return func(s string) string {
			// ScanText returns entity NAMES and never values or
			// offsets, so the only redaction it supports is naming
			// what was found. That is the right shape here anyway: a
			// model asked to judge "a statement containing a
			// taxpayer id" gives the same verdict as one shown the
			// number, and the number never leaves.
			entities := det.ScanText(s)
			if len(entities) == 0 {
				return s
			}
			return s + "\n\n[proxy: this statement contains " +
				strings.Join(entities, ", ") +
				"; the values were withheld]"
		}
	case SendRefuse:
		return func(s string) string {
			if entities := det.ScanText(s); len(entities) > 0 {
				// A sentinel the Evaluator turns into a local
				// denial without any network call.
				return refuseSentinel
			}
			return s
		}
	}
	return nil
}

// refuseSentinel marks content that must not be transmitted.
// refuseSentinel is analyzer.RefuseSentinel, aliased so this file reads
// without qualification.
const refuseSentinel = analyzer.RefuseSentinel

// httpCodecFactory builds a codec factory for a lane's capture settings.
//
// Returns nil when the lane wants the registry default, which keeps the Gate
// on its original path for every lane that did not ask for anything.
func httpCodecFactory(proto hoopinspect.Protocol, h *HTTPCodecConfig) func() hoopinspect.Codec {
	if h == nil || proto != hoopinspect.HTTP {
		return nil
	}
	return newHTTPCodec(*h)
}

// validateAIRules checks each ai_analysis rule against the analyzer section
// and against the lane's OPA settings.
//
// Every refusal here is a control that would otherwise load, evaluate and do
// nothing: the exact failure the pii-entity check exists to prevent, applied
// to a feature that also costs money when it does fire.
func validateAIRules(rules []policy.Rule, cfg *AnalyzerConfig, pc PolicyConfig, lane string) []string {
	gated := pc.OPA.enabled() && pc.OPA.Gate

	if len(rules) == 0 {
		if gated {
			// A gate answers "is this worth a model call" for an
			// analyzer that is not there. It would cost a round trip
			// per statement and change nothing.
			return []string{fmt.Sprintf(
				"%s: policy.opa.gate is on but the lane has no ai_analysis rule, "+
					"so the extra decision would gate nothing", lane)}
		}
		return nil
	}
	var problems []string

	if cfg == nil {
		problems = append(problems, fmt.Sprintf(
			"%s: has ai_analysis rule(s) but the config has no \"analyzer\" section", lane))
	}

	for _, r := range rules {
		if r.Trigger.IsZero() && !gated {
			// An empty trigger classifies nothing. Silently accepting
			// it leaves an operator believing a guardrail is running.
			//
			// A gated lane is the exception: there the gate decides
			// what gets classified, and an empty trigger is how an
			// operator says so.
			problems = append(problems, fmt.Sprintf(
				"%s: ai_analysis rule %q has no trigger, so it would classify nothing; "+
					"name operations, tables or resources, or turn on policy.opa.gate "+
					"and let the policy decide", lane, r.Name))
		}

		if r.Action != "" {
			// policy.newRules refuses this, but an ai_analysis rule
			// never reaches it: splitAnalyzerRules lifts these out
			// first, so the check there is unreachable from a config
			// file and the field is read by nobody. Accepting it
			// leaves an operator believing they deferred a rule that
			// is still deciding for itself.
			problems = append(problems, fmt.Sprintf(
				"%s: ai_analysis rule %q sets action %q, which this rule type ignores; "+
					"it defers per risk level through high, medium and low",
				lane, r.Name, r.Action))
		}

		named := false
		for level, raw := range map[string]string{
			"high": r.HighRisk, "medium": r.MediumRisk, "low": r.LowRisk,
		} {
			if raw == "" {
				continue
			}
			named = true
			a := analyzer.Action(raw)
			if !a.Valid() {
				problems = append(problems, fmt.Sprintf(
					"%s: ai_analysis rule %q: unknown action %q for %s risk "+
						"(allow, warn, block or defer)", lane, r.Name, raw, level))
				continue
			}
			if a == analyzer.ActionDefer && !pc.OPA.enabled() {
				// Deferring to a decision that does not exist allows
				// everything. The operator asked for the opposite.
				problems = append(problems, fmt.Sprintf(
					"%s: ai_analysis rule %q defers %s risk to a policy decision, "+
						"and the lane has no policy.opa.url to defer to; "+
						"set one or use block, warn or allow",
					lane, r.Name, level))
			}
			// require_review needs an evaluator after this one, exactly as
			// defer does. Its refusal lives in validateReviewRules, which
			// owns the review section; naming it here too would put the
			// same check in two places.
		}
		if !named {
			problems = append(problems, fmt.Sprintf(
				"%s: ai_analysis rule %q names no action for any risk level, "+
					"so every verdict would allow", lane, r.Name))
		}
	}
	return problems
}
