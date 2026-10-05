package daemon

import (
	"context"
	"crypto/x509"
	"fmt"
	"math"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/inspect"

	"github.com/hoophq/hoop/sidecar/policy"
	codecssh "github.com/hoophq/libhoop/v2/codec/ssh"
)

// HTTPCodecConfig controls what a lane's HTTP codec exposes to policy.
//
// It exists because the codec's capture options are a per-lane decision the
// registry cannot express: codec/http registers a factory taking no
// arguments, so every lane in the process shared one zero-value Options and
// no lane could see a request body or a header.
//
// The defaults expose nothing, with one exception: a lane whose analyzer holds
// for review captures request bodies, because the reviewer must read what the
// approval releases. Everything else is an explicit act, because everything
// captured reaches the policy engine, the audit trail and, where an analyzer
// is configured, a third party: the analyzer renders the allowlisted headers
// into its prompt beside the request line and the body.
type HTTPCodecConfig struct {
	// CaptureBody includes request and response bodies in the Statement.
	// Optional for the analyzer: it judges a bodiless request from its path
	// and headers, and without this never sees what a POST or a PUT
	// carries. A lane that holds for review captures request bodies anyway.
	CaptureBody bool `json:"capture_body,omitempty" label:"Capture the request body" help:"Lets policy and the AI analyzer read what a POST or PUT carries. A listener that holds for review captures request bodies without it."`

	// MaxBodyBytes truncates a captured body. Zero uses the codec default.
	MaxBodyBytes int `json:"max_body_bytes,omitempty" label:"Max body bytes" help:"0 uses the codec default of 64 KiB."`

	// Headers names the headers to expose, matched case-insensitively.
	// There is no capture-all. An http_header rule on the lane may only
	// name headers listed here; the config is refused otherwise.
	Headers []string `json:"headers,omitempty" label:"Headers" help:"Allowlist exposed to policy; an http_header rule may only name these. Authorization, cookie, proxy-authorization and set-cookie are always refused."`

	// SensitiveQueryParams adds query parameter names whose value the codec
	// redacts before anything sees the request: the audit trail, OPA, the
	// analyzer. The codec already redacts the common credential names
	// (access_token, api_key, sig, X-Amz-Signature, ...); this list widens
	// that for a deployment's own spelling. There is no way to narrow it.
	SensitiveQueryParams []string `json:"sensitive_query_params,omitempty" cap:"sensitive_query_params" since:"1.184.2" label:"Sensitive query parameters" help:"Extra parameter names whose value is redacted before anything sees the request."`
}

// headerNames is the allowlist as the codec receives it: trimmed and
// lowercased. One normalization serves the validator, the refusal list and
// the codec, so a name with stray whitespace cannot pass the first and be
// dropped by the last.
func (h *HTTPCodecConfig) headerNames() []string {
	if h == nil {
		return nil
	}
	out := make([]string, 0, len(h.Headers))
	for _, name := range h.Headers {
		out = append(out, strings.ToLower(strings.TrimSpace(name)))
	}
	return out
}

// captures reports whether the lane's codec exposes a header to policy.
func (h *HTTPCodecConfig) captures(header string) bool {
	for _, name := range h.headerNames() {
		if name == header {
			return true
		}
	}
	return false
}

// validateHTTPHeaderRules refuses an http_header rule on an http lane that
// names a header the lane does not capture. The codec drops every header
// outside `http.headers`, so such a rule would load and never match; this
// is the same bargain the config strikes for a pii rule on a grpc lane
// without capture_payload. The message names the header to allowlist,
// because that is the fix.
func validateHTTPHeaderRules(rules []policy.Rule, h *HTTPCodecConfig, lane string) []string {
	var problems []string
	for _, r := range rules {
		for _, header := range r.HeaderNames() {
			if h.captures(header) {
				continue
			}
			problems = append(problems, fmt.Sprintf(
				"%s: rule %q reads header %q, which the listener does not capture, "+
					"so it would never match; add it to the listener's http.headers",
				lane, r.Name, header))
		}
	}
	return problems
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

// AnalyzerConfig configures the AI risk analyzer: the provider for the whole
// process, and the defaults every listener's analyzer block inherits.
//
// One provider serves every lane. Provider, model, endpoint and credential
// are process-wide because a second provider per lane would double the
// credential surface for a case nobody has asked for; everything else here
// is a DEFAULT a listener's own analyzer block overrides per lane.
type AnalyzerConfig struct {
	// Provider names a registered provider: anthropic, openai, gemini,
	// vertex. Availability depends on what the binary links.
	Provider string `json:"provider,omitempty"`

	// Model names the model. Provider-specific format.
	Model string `json:"model,omitempty"`

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

	// Extra carries provider-specific settings: Vertex's project, region
	// and publisher; Gemini's api.
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
	Cache AnalyzerCacheConfig `json:"cache,omitzero"`

	// MaxCalls bounds classifications for the process lifetime. Zero is
	// unbounded. A backstop against a pathological workload, not a quota.
	MaxCalls int `json:"max_calls,omitempty"`

	// RateLimit bounds how fast classifications are spent. Nil is no
	// limit. It sits beside MaxCalls: a call passes both, and a spike the
	// rate throttles resumes on its own where a spent max_calls lasts until
	// a restart.
	RateLimit *AnalyzerRateLimitConfig `json:"rate_limit,omitempty" cap:"analyzer_rate_limit"`

	// MaxOutputTokens bounds the model's reply. Zero uses the provider
	// default.
	MaxOutputTokens int `json:"max_output_tokens,omitempty"`

	// Temperature, TopP, TopK and Seed are the model's sampling
	// parameters. Unset sends nothing and the model's default applies.
	// Pointers, because 0 is a value: temperature 0 is the most
	// deterministic setting. A provider whose API lacks one refuses it at
	// startup (Anthropic has no seed, OpenAI no top_k). Gemini 3 and later
	// models accept temperature, top_p and top_k and ignore them.
	//
	// Process-wide like the model, because sampling is a property of the
	// model call, not of a lane.
	Temperature *float64 `json:"temperature,omitempty" cap:"analyzer_sampling"`
	TopP        *float64 `json:"top_p,omitempty" cap:"analyzer_sampling"`
	TopK        *int     `json:"top_k,omitempty" cap:"analyzer_sampling"`
	Seed        *int64   `json:"seed,omitempty" cap:"analyzer_sampling"`

	// MaxRetries re-sends a classification after a 408, 429 or 5xx answer,
	// up to this many times, inside the same timeout_sec. Zero sends once.
	MaxRetries int `json:"max_retries,omitempty" cap:"analyzer_retries"`
}

// sampling is the section's sampling parameters as the provider takes them.
func (a *AnalyzerConfig) sampling() analyzer.Sampling {
	return analyzer.Sampling{Temperature: a.Temperature, TopP: a.TopP, TopK: a.TopK, Seed: a.Seed}
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

// AnalyzerRateLimitConfig bounds how fast an analyzer spends: calls per
// per_sec seconds, drawn from a bucket holding at most burst.
//
// A POINTER wherever it appears, so an omitted limit writes no key. The
// control plane serves these documents to builds that decode strictly, and
// a `"rate_limit":{}` an older build cannot read would refuse the whole
// document; see CheckServable.
type AnalyzerRateLimitConfig struct {
	Calls  int `json:"calls,omitempty"`
	PerSec int `json:"per_sec,omitempty"`

	// Burst is how many calls may go out back to back after a quiet
	// period. Zero uses calls.
	Burst int `json:"burst,omitempty"`
}

// maxRateLimitPerSec is the largest per_sec a time.Duration holds, about 292
// years. limit multiplies by time.Second, and past this the product wraps:
// just past it goes negative, and near twice it wraps back to a small
// positive period that enforces a far faster rate than the config says.
const maxRateLimitPerSec = math.MaxInt64 / int64(time.Second)

// limit is the analyzer's form of the config. It refuses a per_sec past
// maxRateLimitPerSec itself rather than trusting validate ran first: a
// Config assembled in Go reaches the lane build without LoadConfigBytes.
func (r AnalyzerRateLimitConfig) limit() (analyzer.RateLimit, error) {
	if int64(r.PerSec) > maxRateLimitPerSec {
		return analyzer.RateLimit{}, fmt.Errorf(
			"rate_limit.per_sec %d is past the largest period this build holds (%d)",
			r.PerSec, maxRateLimitPerSec)
	}
	return analyzer.RateLimit{
		Calls: r.Calls,
		Per:   time.Duration(r.PerSec) * time.Second,
		Burst: r.Burst,
	}, nil
}

// validate checks one rate_limit block as written: negatives, and a block
// that names nothing. Whether calls and per_sec arrive together is a fact
// about the EFFECTIVE limit, since a lane may name one and inherit the
// other; validateEffective answers that.
func (r *AnalyzerRateLimitConfig) validate(where string) []string {
	if r == nil {
		return nil
	}
	var problems []string
	for _, f := range [...]struct {
		name string
		v    int
	}{{"calls", r.Calls}, {"per_sec", r.PerSec}, {"burst", r.Burst}} {
		if f.v < 0 {
			problems = append(problems, fmt.Sprintf("%s: rate_limit.%s is negative", where, f.name))
		}
	}
	if _, err := r.limit(); err != nil {
		problems = append(problems, where+": "+err.Error())
	}
	if *r == (AnalyzerRateLimitConfig{}) {
		problems = append(problems, where+
			": rate_limit names none of calls, per_sec or burst, so it limits nothing")
	}
	return problems
}

// validateEffective refuses a resolved limit that would load and bound
// nothing: a rate with no period, a period with no rate, a burst with
// neither. Negatives are validate's to report.
func (r AnalyzerRateLimitConfig) validateEffective(where string) []string {
	if r.Calls < 0 || r.PerSec < 0 || r.Burst < 0 {
		return nil
	}
	switch {
	case (r.Calls > 0) != (r.PerSec > 0):
		return []string{where + ": rate_limit needs both calls and per_sec"}
	case r.Burst > 0 && r.Calls == 0:
		return []string{where + ": rate_limit.burst needs calls and per_sec"}
	}
	return nil
}

// resolveRateLimit is a lane's EFFECTIVE rate: the top-level default with
// each field the lane block names laid over it. Field by field, like cache,
// so a block naming only calls keeps the inherited period.
func resolveRateLimit(cfg *AnalyzerConfig, la *LaneAnalyzerConfig) AnalyzerRateLimitConfig {
	var out AnalyzerRateLimitConfig
	if cfg != nil && cfg.RateLimit != nil {
		out = *cfg.RateLimit
	}
	if la == nil || la.RateLimit == nil {
		return out
	}
	if la.RateLimit.Calls != 0 {
		out.Calls = la.RateLimit.Calls
	}
	if la.RateLimit.PerSec != 0 {
		out.PerSec = la.RateLimit.PerSec
	}
	if la.RateLimit.Burst != 0 {
		out.Burst = la.RateLimit.Burst
	}
	return out
}

// resolveMaxCalls is a lane's effective max_calls: the block's, else the
// top-level default.
func resolveMaxCalls(cfg *AnalyzerConfig, la *LaneAnalyzerConfig) int {
	if la != nil && la.MaxCalls != 0 {
		return la.MaxCalls
	}
	if cfg == nil {
		return 0
	}
	return cfg.MaxCalls
}

// rateLimitNotes says what an analyzer may spend at its effective rate, and
// where that rate meets max_calls: a cap the rate reaches in a few hours
// turns the throttle into an outage, and a cap below the burst means the
// rate never fires. who names the analyzer in the note.
func rateLimitNotes(cfg *AnalyzerConfig, la *LaneAnalyzerConfig, who string) []string {
	rate := resolveRateLimit(cfg, la)
	lim, err := rate.limit()
	if err != nil || !lim.Enabled() {
		return nil
	}
	burst := lim.Capacity()
	// The bucket starts full and refills at the rate, so a day can spend
	// the burst plus a day of refill and never more.
	perDay := int64(burst) + int64(rate.Calls)*86400/int64(rate.PerSec)
	notes := []string{fmt.Sprintf(
		"%s spends at most %d model calls in any 24h: rate_limit %d per %ds, burst %d",
		who, perDay, rate.Calls, rate.PerSec, burst)}

	switch maxCalls := resolveMaxCalls(cfg, la); {
	case maxCalls == 0:
	case maxCalls <= burst:
		notes = append(notes, fmt.Sprintf(
			"%s: max_calls %d is at or below the rate_limit burst %d, so the rate "+
				"limit never refuses a call", who, maxCalls, burst))
	default:
		left := time.Duration(float64(maxCalls-burst) * float64(lim.Per) / float64(rate.Calls))
		notes = append(notes, fmt.Sprintf(
			"%s: at the full rate, max_calls %d runs out after %s; after that "+
				"nothing is classified until a restart", who, maxCalls, left.Round(time.Second)))
	}
	return notes
}

// LaneAnalyzerConfig is one listener's own analyzer block: what this lane
// classifies, what each risk level does, and which process defaults it
// overrides.
//
// The analyzer is a per-lane component, a peer of guardrails and mask,
// not a guardrail rule. The block holds what is genuinely per lane — the
// trigger, the risk-to-action map, the prompt — and inherits everything
// else from the top-level analyzer section, which keeps the provider, the
// model and the credential: one provider, one credential read per process.
//
// The DEPRECATED spelling is a `type: ai_analysis` rule under
// guardrails.rules. It still loads and still works (normalize records a
// deprecation naming this block), because removing it would break every
// deployed config on upgrade rather than warning about it.
type LaneAnalyzerConfig struct {
	// Trigger narrows which statements this lane classifies. OMITTED, the
	// lane classifies everything: declaring the analyzer is the opt-in,
	// max_calls and the cache bound the bill, and -validate prints the
	// per-statement cost as a note. On a lane with opa.gate the gate-phase
	// policy decides instead, and an omitted trigger leaves it fully in
	// charge.
	Trigger *policy.AITrigger `json:"trigger,omitempty"`

	// HighRisk, MediumRisk and LowRisk map a verdict onto an action:
	// allow, warn, block or defer. An unset level defaults to allow, so an
	// operator opts into blocking a tier by naming it.
	HighRisk   string `json:"high,omitempty"`
	MediumRisk string `json:"medium,omitempty"`
	LowRisk    string `json:"low,omitempty"`

	// Prompt replaces the inherited risk guidance for this lane. This is
	// where protocol-specific wording belongs: analyzer.prompt reaches
	// every lane, so SQL advice written there follows an HTTP body to the
	// model. The output contract (call exactly one tool, never quote a
	// literal value) is appended after it and cannot be removed.
	Prompt string `json:"prompt,omitempty"`

	// Message reaches the user on denial. Empty falls back to the model's
	// own title.
	Message string `json:"message,omitempty"`

	// ApprovalRule names the control plane access request rule that decides
	// who may approve a statement this lane holds. The rule carries the
	// reviewer groups, the approval count and the force-approval list; this
	// field carries only its name, because the sidecar never holds that
	// policy and the control plane authorizes the review against the stored
	// config.
	//
	// PER LANE, not process-wide: the people who may release a statement
	// against the payments database are not the people who may release one
	// against a reporting replica, and an inherited default would make the
	// looser of the two the accident.
	//
	// It is meaningful only where a risk level asks for require_review, and
	// required there: the two arrive together or startup refuses the lane,
	// because either one alone is a control nothing reads.
	ApprovalRule string `json:"approval_rule,omitempty"`

	// ReviewMode is what a held statement does while its review is
	// pending: "hold" (the default) waits on the connection, "return"
	// denies at once with the review id so an agent can resend the
	// identical statement after approval. It applies to every client on
	// the lane, humans included (ADR-0021).
	//
	// omitempty is load-bearing: a build that predates the field decodes
	// the served document strictly, so a hold lane must never carry the
	// key. The cap tag names what a build reports on its handshake, so the
	// control plane refuses to serve "return" to a build without it; see
	// CheckServable. Retire it through normalize and Deprecations, never by
	// deleting it.
	ReviewMode analyzer.ReviewMode `json:"review_mode,omitempty" cap:"review_mode" since:"1.196.0"`

	// The rest override the top-level analyzer defaults for this lane.
	// A zero value inherits; see the field of the same name on
	// AnalyzerConfig for what each bounds.
	TimeoutSec    int                      `json:"timeout_sec,omitempty"`
	FailOpen      *bool                    `json:"fail_open,omitempty"`
	Send          SendMode                 `json:"send,omitempty"`
	MaxInputBytes int                      `json:"max_input_bytes,omitempty"`
	MaxCalls      int                      `json:"max_calls,omitempty"`
	RateLimit     *AnalyzerRateLimitConfig `json:"rate_limit,omitempty" cap:"analyzer_rate_limit"`
	Cache         *AnalyzerCacheConfig     `json:"cache,omitempty"`
}

// specFromRule maps a DEPRECATED ai_analysis rule onto the lane block
// shape, which is how the rule form "keeps working": both spellings build
// through buildAnalyzerEvaluator, so they cannot drift apart. A rule
// carries no per-lane overrides, so every zero field inherits the
// top-level defaults exactly as it always did.
func specFromRule(r policy.Rule) LaneAnalyzerConfig {
	return LaneAnalyzerConfig{
		Trigger:    r.Trigger,
		HighRisk:   r.HighRisk,
		MediumRisk: r.MediumRisk,
		LowRisk:    r.LowRisk,
		Prompt:     r.Prompt,
		Message:    r.Message,
	}
}

// failOpen resolves the pointer default.
func (a *AnalyzerConfig) failOpen() bool {
	if a == nil || a.FailOpen == nil {
		return true
	}
	return *a.FailOpen
}

// validate checks the analyzer section in isolation. Off the sidecar host it
// skips whether the provider is linked: only the sidecar binary knows that.
func (a *AnalyzerConfig) validate(hasScanner, onHost bool) []string {
	if a == nil {
		return nil
	}
	var problems []string

	if a.Provider == "" {
		problems = append(problems, "analyzer: no provider set")
	} else if onHost && !providerLinked(a.Provider) {
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
	// The top level is the effective limit for every lane that names none,
	// the deprecated rule form included, so it has to stand on its own.
	if a.RateLimit != nil {
		problems = append(problems, a.RateLimit.validate("analyzer")...)
		problems = append(problems, a.RateLimit.validateEffective("analyzer")...)
	}
	if a.MaxOutputTokens < 0 {
		problems = append(problems, "analyzer: max_output_tokens is negative")
	}
	if err := a.sampling().Validate(); err != nil {
		for _, line := range strings.Split(err.Error(), "\n") {
			problems = append(problems, "analyzer: "+line)
		}
	}
	if a.MaxRetries < 0 {
		problems = append(problems, "analyzer: max_retries is negative")
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
// read, one token source. roots is the process trust pool; the provider
// sends its model calls and any token mint through a client built on it.
func buildAnalyzer(cfg *AnalyzerConfig, roots *x509.CertPool) (analyzer.Provider, error) {
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
		Sampling:        cfg.sampling(),
		HTTPClient:      outboundHTTPClient(roots),
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

// budgetFor returns the process-lifetime purse for one budget key, creating
// it on first sight. Keys are namespaced by analyzer form — see the budget
// key prefixes — and the sharing contract lives on analyzerDeps.budgets.
func (ac *analyzerDeps) budgetFor(key string) *analyzer.Budget {
	if ac.budgets == nil {
		ac.budgets = map[string]*analyzer.Budget{}
	}
	cell, ok := ac.budgets[key]
	if !ok {
		cell = new(analyzer.Budget)
		ac.budgets[key] = cell
	}
	return cell
}

// rateLimitLog reports an analyzer's rate-limit edges on the process log:
// the first refused call, and the first call granted after it. Nil without
// a logger, which is every build that never serves (-validate, tests).
func (ac *analyzerDeps) rateLimitLog(name string) func(limited bool, refused int64) {
	if ac.log == nil {
		return nil
	}
	log := ac.log.With("analyzer", name)
	return func(limited bool, refused int64) {
		if limited {
			log.Warn("analyzer rate limit reached; statements are not classified until it refills")
			return
		}
		log.Info("analyzer rate limit cleared", "refused", refused)
	}
}

// reviewerFor returns the backend this lane holds statements against for human
// approval, or nil when nothing should be filed.
//
// Nil in three cases, and each one denies rather than forwards:
//
//   - The lane names no approval_rule, so no level on it holds.
//   - The lane is OBSERVING. A dry run that paged approvers would be a dry
//     run with consequences, and the statement runs anyway: policy.Observe
//     turns the hold's denial into an allow annotated would_deny, which is
//     the record the mode exists to produce.
//   - The process has no control plane, which is every -validate run.
func (ac *analyzerDeps) reviewerFor(listener string, la *LaneAnalyzerConfig, observing bool) analyzer.Reviewer {
	if ac == nil || la == nil || la.ApprovalRule == "" || observing {
		return nil
	}
	return ac.cp.reviewer(listener, la.ApprovalRule)
}

// budget key prefixes. The map in analyzerDeps is process-wide and keyed by
// name, and the two analyzer forms draw their names from different
// namespaces: a rule is named by the operator, a block by its listener. A
// rule that happens to carry a listener's name must not spend that lane's
// allowance, so each form prefixes its keys and the collision cannot be
// spelled.
const (
	budgetRulePrefix = "rule:"
	budgetLanePrefix = "lane:"
)

// buildAnalyzerEvaluators turns DEPRECATED ai_analysis rules into
// evaluators.
//
// Each rule becomes its own Evaluator, so two rules on one lane get their own
// trigger, action map and denial message while sharing the provider and,
// through the provider, the credential. The call BUDGET comes from ac's
// per-name registry, so a rebuilt evaluator (a hot reload that edited the
// rule's lane) continues the running count instead of starting a fresh one,
// and two lanes naming one rule still pay from one purse.
//
// A rule never holds a statement: it carries no approval_rule to name who
// could release one, which is why validateLaneAnalysis refuses require_review
// on this spelling. Each evaluator is therefore built with a nil reviewer.
func buildAnalyzerEvaluators(
	rules []policy.Rule,
	ac *analyzerDeps,
	hasOPA, gated bool,
) ([]policy.Evaluator, error) {
	if len(rules) == 0 {
		return nil, nil
	}
	if ac == nil || ac.cfg == nil || ac.provider == nil {
		return nil, fmt.Errorf(
			"ai_analysis rule %q needs an analyzer section, and none is configured", rules[0].Name)
	}
	out := make([]policy.Evaluator, 0, len(rules))
	for _, r := range rules {
		ev, err := buildAnalyzerEvaluator(r.Name, budgetRulePrefix+r.Name,
			specFromRule(r), ac, hasOPA, gated, nil)
		if err != nil {
			return nil, fmt.Errorf("rule %q: %w", r.Name, err)
		}
		out = append(out, ev)
	}
	return out, nil
}

// buildLaneAnalyzer turns a listener's analyzer block into its evaluator.
//
// The evaluator is named after the LANE, because the block is a per-lane
// component rather than a named rule: that name is what Finding.Rule, the
// ai_rule audit key and the call budget all carry, and it is the identity
// an operator edits. Two lanes therefore never share a budget, which is
// what "per lane" promises.
func buildLaneAnalyzer(
	lane string,
	la *LaneAnalyzerConfig,
	ac *analyzerDeps,
	hasOPA, gated bool,
	review analyzer.Reviewer,
) (policy.Evaluator, error) {
	if la == nil {
		return nil, nil
	}
	if ac == nil || ac.cfg == nil || ac.provider == nil {
		return nil, fmt.Errorf(
			"listener %q has an analyzer block, and the config has no top-level "+
				"analyzer section to supply the provider", lane)
	}
	ev, err := buildAnalyzerEvaluator(lane, budgetLanePrefix+lane, *la, ac, hasOPA, gated, review)
	if err != nil {
		return nil, fmt.Errorf("analyzer block: %w", err)
	}
	return ev, nil
}

// buildAnalyzerEvaluator is the one place an evaluator is assembled, for
// both spellings. Every zero field in la inherits the top-level analyzer
// default, so the block overrides exactly what it names and nothing else.
//
// name is what the evaluator reports (Finding.Rule, the ai_rule audit key);
// budgetKey is its purse in ac's registry. They differ because the report
// name is an operator-facing identity and the purse must not collide across
// the two analyzer forms — see the budget key prefixes above.
//
// An OMITTED trigger classifies everything — on an ungated lane. Declaring
// an analyzer is already the opt-in, so the absence of a narrower means
// "all of it", bounded by the cache and max_calls; buildLanes leaves a
// startup note naming the cost. A GATED lane keeps the zero trigger, so a
// gate-phase policy stays the only spender and Rego's silence keeps
// meaning "skip" for every deployed two-phase config.
//
// review is what an ActionRequireReview level calls, already carrying the
// listener and the approval rule (see controlPlane.reviewer). Nil where
// nothing should be filed, which denies: see analyzer.Config.Review.
func buildAnalyzerEvaluator(
	name, budgetKey string,
	la LaneAnalyzerConfig,
	ac *analyzerDeps,
	hasOPA, gated bool,
	review analyzer.Reviewer,
) (policy.Evaluator, error) {
	cfg := ac.cfg

	trigger := triggerFrom(la.Trigger)
	if trigger.IsZero() && !gated {
		trigger.All = true
	}

	actions, err := actionMap(la, hasOPA)
	if err != nil {
		return nil, err
	}
	// Lane prompt beats the analyzer default beats the built-in.
	guidance := la.Prompt
	if guidance == "" {
		guidance = cfg.Prompt
	}
	timeout := la.TimeoutSec
	if timeout == 0 {
		timeout = cfg.TimeoutSec
	}
	failOpen := cfg.failOpen()
	if la.FailOpen != nil {
		failOpen = *la.FailOpen
	}
	maxInput := la.MaxInputBytes
	if maxInput == 0 {
		maxInput = cfg.MaxInputBytes
	}
	maxCalls := resolveMaxCalls(cfg, &la)
	rate, err := resolveRateLimit(cfg, &la).limit()
	if err != nil {
		return nil, err
	}
	// Cache fields merge INDIVIDUALLY, like every other override here: a
	// block naming only ttl_sec keeps the inherited size. Replacing the
	// struct wholesale would zero the field the block did not write, and a
	// zero on either side disables caching — the opposite of what a partial
	// override asked for.
	cache := cfg.Cache
	if la.Cache != nil {
		if la.Cache.Size != 0 {
			cache.Size = la.Cache.Size
		}
		if la.Cache.TTLSec != 0 {
			cache.TTLSec = la.Cache.TTLSec
		}
	}
	send := la.Send
	if send == "" {
		send = cfg.Send
	}
	// The EFFECTIVE mode is what has to be checked against the detector,
	// because a lane can override the top-level send. The top-level check
	// in AnalyzerConfig.validate cannot see overrides, and a nil redactor
	// under redacted or refuse would transmit the original text under a
	// name that promises otherwise.
	switch send {
	case SendRedacted, SendRefuse:
		if ac.det == nil {
			return nil, fmt.Errorf(
				"send: %s needs a detector, and this build has none", send)
		}
	}
	return analyzer.New(analyzer.Config{
		Rule:          name,
		Provider:      ac.provider,
		Guidance:      guidance,
		Actions:       actions,
		Trigger:       trigger,
		Message:       la.Message,
		Timeout:       time.Duration(timeout) * time.Second,
		MaxRetries:    cfg.MaxRetries,
		FailOpen:      failOpen,
		MaxInputBytes: maxInput,
		CacheSize:     cache.Size,
		CacheTTL:      time.Duration(cache.TTLSec) * time.Second,
		MaxCalls:      maxCalls,
		RateLimit:     rate,
		OnRateLimit:   ac.rateLimitLog(name),
		OnCall:        ac.callObserver(),
		OnOutcome:     ac.outcomeObserver(),
		Budget:        ac.budgetFor(budgetKey),
		Redact:        redactorFor(send, ac.det),
		Review:        review,
		ReviewMode:    la.ReviewMode,
		ReturnNext:    returnNext(ac.mcp),
	})
}

// returnNext is the step a return-mode denial names before the resend: the
// MCP wait, when this process serves it.
func returnNext(mcp bool) string {
	if !mcp {
		return ""
	}
	return "call the MCP tool " + ReviewWaitTool + " with the review id"
}

func triggerFrom(t *policy.AITrigger) analyzer.Trigger {
	if t == nil {
		return analyzer.Trigger{}
	}
	return analyzer.Trigger{
		Operations: t.Operations,
		Tables:     t.Tables,
		Resources:  t.Resources,
		Any:        triggerItems(t.Any),
		Exclude:    triggerItems(t.Exclude),
	}
}

func triggerItems(items []policy.AITriggerItem) []analyzer.TriggerItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]analyzer.TriggerItem, len(items))
	for i, item := range items {
		out[i] = analyzer.TriggerItem{
			Operations: item.Operations,
			Tables:     item.Tables,
			Resources:  item.Resources,
		}
	}
	return out
}

// actionMap turns the block's high/medium/low strings into the analyzer's
// action vocabulary.
//
// hasOPA false degrades `defer` to `block`. Deferring names a decision-maker,
// and the only evaluator that reads an ai_analysis finding is a decide-phase
// OPA client. With no OPA the finding would be recorded and read by nobody,
// which allows the statement: the opposite of what the operator asked for.
// The lane keeps a startup note saying so, because a config that reads
// `high: defer` and behaves as `high: block` has to say which one it did.
func actionMap(la LaneAnalyzerConfig, hasOPA bool) (analyzer.ActionMap, error) {
	m := analyzer.ActionMap{}
	for level, raw := range map[analyzer.RiskLevel]string{
		analyzer.RiskHigh:   la.HighRisk,
		analyzer.RiskMedium: la.MediumRisk,
		analyzer.RiskLow:    la.LowRisk,
	} {
		if raw == "" {
			continue
		}
		a := analyzer.Action(raw)
		if !a.Valid() {
			return nil, fmt.Errorf("unknown action %q for %s risk", raw, level)
		}
		if a == analyzer.ActionDefer && !hasOPA {
			a = analyzer.ActionBlock
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
	if problems := cfg.Analyzer.validate(det != nil, true); len(problems) > 0 {
		return nil, fmt.Errorf("invalid config:\n  - %s", strings.Join(problems, "\n  - "))
	}

	roots, err := loadTrustRoots(cfg.Trust)
	if err != nil {
		return nil, err
	}
	provider, err := buildAnalyzer(cfg.Analyzer, roots)
	if err != nil {
		return nil, err
	}
	return &analyzerDeps{
		cfg:      cfg.Analyzer,
		provider: provider,
		cp:       cfg.cp,
		det:      det,
		mcp:      cfg.MCP != nil,
		metrics:  newAnalyzerMetrics(cfg.Analyzer.Provider, cfg.Analyzer.Model),
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
			// RedactText rewrites every detected span as its entity
			// class, so the value itself never leaves the process. The
			// note tells the model what it is looking at: a statement
			// judged as "contains <US_SSN>" classifies the same as one
			// shown the number, and the number stays here.
			redacted, entities := det.RedactText(s)
			if len(entities) == 0 {
				return s
			}
			return redacted + "\n\n[proxy: this statement contained " +
				strings.Join(entities, ", ") +
				"; each value was replaced with its entity class]"
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

// validateLaneAnalysis checks a lane's analyzer surface — its own analyzer
// block and any DEPRECATED ai_analysis rules — against the top-level
// analyzer section and the lane's OPA settings.
//
// Every refusal here is a control that would otherwise load, evaluate and do
// nothing: the exact failure the pii-entity check exists to prevent, applied
// to a feature that also costs money when it does fire.
//
// lc is read only by ValidateHoldOnLane: the analyzer block cannot see which
// lane it is on. A hold's OTHER prerequisite, a control plane to file with,
// is checked in buildLanes; see holdsWithoutAPlane.
func validateLaneAnalysis(rules []policy.Rule, la *LaneAnalyzerConfig,
	cfg *AnalyzerConfig, opa *OPAConfig, lane string, lc ListenerConfig) []string {
	gated := opa.enabled() && opa.Gate

	if len(rules) == 0 && la == nil {
		if gated {
			// A gate answers "is this worth a model call" for an
			// analyzer that is not there. It would cost a round trip
			// per statement and change nothing.
			return []string{fmt.Sprintf(
				"%s: opa.gate is on but the lane has no analyzer block, "+
					"so the extra decision would gate nothing", lane)}
		}
		return nil
	}
	var problems []string

	if cfg == nil {
		what := "has ai_analysis rule(s)"
		if la != nil {
			what = "has an analyzer block"
		}
		problems = append(problems, fmt.Sprintf(
			"%s: %s but the config has no top-level \"analyzer\" section "+
				"(the provider, the model and the credential live there)", lane, what))
	}

	if la != nil {
		problems = append(problems, validateLaneBlock(la, lane)...)
		problems = append(problems, validateGatedExclude(la.Trigger, gated, lane+": analyzer block")...)
		problems = append(problems, ValidateHoldOnLane(la, lc, lane+": analyzer block")...)
		// Only where the block names a rate: otherwise the effective one
		// is the top level's, which AnalyzerConfig.validate already said.
		if la.RateLimit != nil {
			problems = append(problems, resolveRateLimit(cfg, la).validateEffective(
				lane+": analyzer block")...)
		}
	}

	for _, r := range rules {
		// An omitted trigger is legal on either lane kind: an ungated
		// lane classifies everything (buildLanes leaves a cost note), a
		// gated one hands the question to the gate-phase policy.
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

		where := fmt.Sprintf("%s: ai_analysis rule %q", lane, r.Name)
		problems = append(problems, validateRiskActions(
			r.HighRisk, r.MediumRisk, r.LowRisk, where)...)
		problems = append(problems, validateTriggerItems(r.Trigger, where)...)
		problems = append(problems, validateGatedExclude(r.Trigger, gated, where)...)
		problems = append(problems, refuseRuleFormHold(
			r.HighRisk, r.MediumRisk, r.LowRisk, where)...)
	}
	return problems
}

// refuseRuleFormHold refuses require_review on the DEPRECATED ai_analysis
// rule, which the listener analyzer block supports.
//
// The rule carries no approval_rule and cannot: specFromRule maps a rule onto
// the block shape and leaves the field empty, so a holding rule would file a
// review naming nobody who could release it. The message names the block
// rather than offering block or defer, because moving there is both the fix
// for this and the direction the config is going anyway.
func refuseRuleFormHold(high, medium, low, where string) []string {
	for _, level := range [...]struct{ name, action string }{
		{"high", high}, {"medium", medium}, {"low", low},
	} {
		if analyzer.Action(level.action) != analyzer.ActionRequireReview {
			continue
		}
		return []string{fmt.Sprintf(
			"%s asks for %q on %s risk, which a deprecated ai_analysis rule cannot "+
				"do: it carries no approval_rule to name who may release a held "+
				"statement. Move this lane to a listener \"analyzer\" block, which "+
				"takes both", where, level.action, level.name)}
	}
	return nil
}

// ValidateLaneAnalyzerBlock checks one listener's analyzer block the way this
// process checks it at startup: the risk vocabulary, the send mode, the
// numeric bounds, and the pairing a hold needs -- require_review on a level
// and an approval rule saying who may release the statement.
//
// Exported for the control plane, which stores these blocks and distributes
// them. The same refusal costs a 422 an admin reads at the save, or a fleet
// that crash-loops at its next restart. Same reason ValidateSSHMasking is
// exported, and the same contract: this is the authority, not a copy of it.
//
// The lane's block ALONE. validateLaneAnalysis also reads the top-level
// analyzer section, the lane's OPA settings and the deprecated ai_analysis
// rules; none of those is distributed, and it would refuse over their absence
// a block the control plane composes correctly.
func ValidateLaneAnalyzerBlock(la *LaneAnalyzerConfig, lane string) []string {
	if la == nil {
		return nil
	}
	return validateLaneBlock(la, lane)
}

// ValidateHoldOnLane is the half of a hold only the lane answers: an ssh lane
// that admits a shell. A shell sends no statements (ADR-0015), so a user
// types in it what exec would have held. Refused rather than noted: the
// operator asked for a human gate and the shell walks around it.
//
// Exported for the control plane, which knows the lane only once a rule is
// bound to it.
func ValidateHoldOnLane(la *LaneAnalyzerConfig, lc ListenerConfig, where string) []string {
	if la == nil || !analyzerHolds(la) || !isSSH(lc) || !lc.SSH.admits(codecssh.CapShell) {
		return nil
	}
	return []string{fmt.Sprintf(
		"%s asks for %q on an ssh lane that admits shell, and a shell sends no "+
			"statements, so nothing typed in it is held; drop shell from "+
			"ssh.capabilities_allowed", where, analyzer.ActionRequireReview)}
}

// validateLaneBlock checks one listener's analyzer block in isolation. The
// checks mirror the rule-form ones — same failure, same message shape — plus
// the numeric bounds a rule never carried, which get the same negative
// refusal AnalyzerConfig.validate applies to the defaults they override.
func validateLaneBlock(la *LaneAnalyzerConfig, lane string) []string {
	var problems []string
	where := lane + ": analyzer block"
	problems = append(problems, validateRiskActions(
		la.HighRisk, la.MediumRisk, la.LowRisk, where)...)
	problems = append(problems, validateTriggerItems(la.Trigger, where)...)

	switch la.Send {
	case "", SendRaw, SendRedacted, SendRefuse:
	default:
		problems = append(problems, fmt.Sprintf(
			"%s: unknown send mode %q (raw, redacted or refuse)", where, la.Send))
	}
	if la.TimeoutSec < 0 {
		problems = append(problems, where+": timeout_sec is negative")
	}
	if la.MaxInputBytes < 0 {
		problems = append(problems, where+": max_input_bytes is negative")
	}
	if la.MaxCalls < 0 {
		problems = append(problems, where+": max_calls is negative")
	}
	problems = append(problems, la.RateLimit.validate(where)...)
	if la.Cache != nil {
		if la.Cache.Size < 0 {
			problems = append(problems, where+": cache.size is negative")
		}
		if la.Cache.TTLSec < 0 {
			problems = append(problems, where+": cache.ttl_sec is negative")
		}
	}

	// The approval rule and the levels that hold have to arrive together.
	// Either one alone is a control that loads and is read by nobody: a
	// reviewer list nothing consults, or a hold with no one able to
	// release it. The blank case is separate because a name of spaces
	// matches no rule in the control plane and would surface as a refused
	// review long after startup.
	holds := analyzerHolds(la)
	switch {
	case la.ApprovalRule == "" && holds:
		problems = append(problems, fmt.Sprintf(
			"%s asks for %q and names no approval_rule; the rule is what decides who "+
				"may release a held statement, and the control plane refuses a review "+
				"that does not name one", where, analyzer.ActionRequireReview))
	case la.ApprovalRule == "":
	case strings.TrimSpace(la.ApprovalRule) == "":
		problems = append(problems, where+
			": approval_rule is blank; it names an access request rule in the control plane")
	case !holds:
		problems = append(problems, fmt.Sprintf(
			"%s: approval_rule %q names who may approve a statement, and no risk "+
				"level asks for %q, so nothing on this lane would hold one",
			where, la.ApprovalRule, analyzer.ActionRequireReview))
	}

	// Same pairing as approval_rule: a mode on a lane that holds nothing is
	// read by nobody.
	switch {
	case !la.ReviewMode.Valid():
		problems = append(problems, fmt.Sprintf(
			"%s: unknown review_mode %q (hold or return)", where, la.ReviewMode))
	case la.ReviewMode != "" && !holds:
		problems = append(problems, fmt.Sprintf(
			"%s: review_mode %q decides how a held statement waits, and no risk "+
				"level asks for %q, so nothing on this lane would hold one",
			where, la.ReviewMode, analyzer.ActionRequireReview))
	}
	return problems
}

// validateTriggerItems refuses a trigger item that names no field. It would
// check nothing and so match every statement, which an operator writing
// "any" or "exclude" never means.
//
// It also refuses an item operation no codec reports: a typo there matches
// nothing, so its condition never classifies or never excludes. The flat
// lists keep their old, unchecked reading, because a deployed config may
// carry a value that would now refuse to load.
func validateTriggerItems(t *policy.AITrigger, where string) []string {
	if t == nil {
		return nil
	}
	var problems []string
	for _, list := range [...]struct {
		name  string
		items []policy.AITriggerItem
	}{{"any", t.Any}, {"exclude", t.Exclude}} {
		for i, item := range list.items {
			if item.IsZero() {
				problems = append(problems, fmt.Sprintf(
					"%s: trigger.%s[%d] names no operations, tables or resources",
					where, list.name, i))
			}
			for _, op := range item.Operations {
				if !slices.Contains(inspect.Operations(), op) {
					problems = append(problems, fmt.Sprintf(
						"%s: trigger.%s[%d] names unknown operation %q",
						where, list.name, i, op))
				}
			}
		}
	}
	return problems
}

// validateGatedExclude refuses exclude on a gated lane whose trigger selects
// nothing else. The gate decides there: a request replaces the whole trigger
// and silence selects nothing, so exclude is never read.
func validateGatedExclude(t *policy.AITrigger, gated bool, where string) []string {
	if !gated || t == nil || len(t.Exclude) == 0 || !t.IsZero() {
		return nil
	}
	return []string{where + ": trigger.exclude narrows nothing on a lane with " +
		"opa.gate and no other trigger condition; the gate-phase policy decides " +
		"what is classified there"}
}

// ValidateLaneTriggers runs the trigger checks that need a lane's RESOLVED
// OPA settings, over a whole document: exclude with nothing else to narrow on
// a gated lane. The lane may inherit opa.gate from the top level.
//
// Exported for the control plane. A rule is valid alone and the listener it
// binds to decides whether the lane is gated; served unchecked, the sidecar
// refuses the whole document and every lane on it stops reloading.
func ValidateLaneTriggers(cfg Config) []string {
	var problems []string
	for _, lc := range cfg.Listeners {
		gc, opa, _ := cfg.resolve(lc)
		gated := opa.enabled() && opa.Gate
		if lc.Analyzer != nil {
			problems = append(problems, validateGatedExclude(
				lc.Analyzer.Trigger, gated, lc.Name+": analyzer block")...)
		}
		for _, r := range gc.Rules {
			if r.Type == policy.MatchAIAnalysis {
				problems = append(problems, validateGatedExclude(r.Trigger, gated,
					fmt.Sprintf("%s: ai_analysis rule %q", lc.Name, r.Name))...)
			}
		}
	}
	return problems
}

// validateRiskActions checks a high/medium/low action map, shared by the
// block and the rule form so the two spellings refuse identically.
func validateRiskActions(high, medium, low, where string) []string {
	var problems []string
	named := false
	for level, raw := range map[string]string{
		"high": high, "medium": medium, "low": low,
	} {
		if raw == "" {
			continue
		}
		named = true
		a := analyzer.Action(raw)
		if !a.Valid() {
			problems = append(problems, fmt.Sprintf(
				"%s: unknown action %q for %s risk "+
					"(allow, warn, block, defer or require_review)", where, raw, level))
			continue
		}
		// `defer` with no OPA is not a refusal. actionMap degrades it
		// to block, so the statement is denied rather than allowed,
		// and one config file can serve a deployment with OPA and a
		// deployment without one.
		//
		// `require_review` is not refused here either, and its
		// prerequisites are not this function's business: it is legal
		// on an analyzer block and refused on the deprecated rule
		// form, and only the caller knows which it is reading.
	}
	if !named {
		problems = append(problems, fmt.Sprintf(
			"%s names no action for any risk level, "+
				"so every verdict would allow", where))
	}
	return problems
}
