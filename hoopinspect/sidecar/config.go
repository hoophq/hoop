package sidecar

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/hoophq/hoop/hoopinspect"
	"github.com/hoophq/hoop/hoopinspect/analyzer"
	"github.com/hoophq/hoop/hoopinspect/gate"
	"github.com/hoophq/hoop/hoopinspect/policy"
)

// Config is the on-disk configuration.
//
// JSON is the native syntax because the stdlib has no YAML parser and the
// module takes no dependency it can avoid. YAML arrives through the nested module
// github.com/hoophq/hoop/hoopinspect/config/yaml, which transcodes to JSON and
// hands the bytes to LoadConfigBytes. One schema, two syntaxes, and the
// dependency stays out of anything that does not ask for it.
type Config struct {
	// Listeners is the set of protocol endpoints to serve. A sidecar usually
	// runs one; a per-user pod fronting both a database and an API runs two
	// in one process instead of two containers.
	Listeners []ListenerConfig `json:"listeners"`

	// Policy is the DEFAULT rule set and OPA client, applied to every
	// listener that does not override it. See ListenerConfig.Policy.
	Policy PolicyConfig `json:"policy"`

	// Mask is the DEFAULT response rewriting, applied to every listener that
	// does not override it. See ListenerConfig.Mask.
	Mask MaskConfig `json:"mask"`

	// Audit configures where events go.
	Audit AuditConfig `json:"audit"`

	// Admin serves health and stats. Disabled when Listen is empty.
	Admin AdminConfig `json:"admin"`

	// PII configures the optional detector plugin. This package decodes it
	// without interpreting it: knowing what an alcatraz Options looks like
	// would drag back the dependency the split exists to keep out.
	//
	// The field is declared so DisallowUnknownFields does not reject it, and
	// so a build wired without a detector can say "this section needs one"
	// instead of "unknown field pii".
	PII json.RawMessage `json:"pii,omitempty"`

	// Analyzer configures the optional AI risk analyzer.
	//
	// Unlike PII this IS interpreted here, because the shape is small and
	// provider-independent: the provider-specific parts live in Extra, and
	// the credential is a path this package reads rather than material it
	// holds. A provider needing a dependency (Vertex needs GCP OAuth2)
	// stays out via the analyzer registry, not via an opaque section.
	Analyzer *AnalyzerConfig `json:"analyzer,omitempty"`

	// LogLevel is debug, info, warn or error. Default info.
	LogLevel string `json:"log_level"`
}

// ListenerConfig is one protocol endpoint: one Envoy cluster's worth of
// traffic, with its own enforcement stack.
type ListenerConfig struct {
	// Name identifies the listener in logs. Defaults to Connection.
	Name string `json:"name"`

	// Protocol selects the codec: postgres, mssql or http.
	Protocol string `json:"protocol"`

	// Listen is the bind address, or a filesystem path when Network is
	// "unix".
	Listen string `json:"listen"`

	// Network is "tcp" (default) or "unix". Pick a unix socket for a sandbox
	// with no network egress: filesystem permissions decide who can reach the
	// proxy.
	Network string `json:"network"`

	// Upstream is the real backend.
	Upstream string `json:"upstream"`

	// Connection is the operator-facing resource name recorded in audit and
	// exposed to policy. Rules and audit queries key on it; the physical
	// Upstream may change under it.
	Connection string `json:"connection"`

	// UpstreamTLS enables TLS to the backend.
	UpstreamTLS *TLSConfig `json:"upstream_tls"`

	// DownstreamTLS lets the relay terminate the CLIENT's TLS on this lane.
	// Requires cert_file and key_file; the other TLSConfig fields describe an
	// outbound connection and are ignored here.
	//
	// Only `postgres` supports it, and only because pgwire leaves nobody else
	// able to: its TLS is negotiated in-band with an 8-byte SSLRequest, so a
	// plain TLS listener in front cannot terminate it. Envoy's own postgres
	// filter can, but it is contrib-only, marked work-in-progress, and gives
	// up permanently the moment a client asks for GSS encryption, which is
	// what psql does by default whenever a Kerberos ticket is present.
	//
	// Omitting it keeps the documented posture: the relay terminates no
	// downstream TLS and whatever fronts it owns that leg.
	DownstreamTLS *TLSConfig `json:"downstream_tls"`

	// IdentityHeader names an HTTP header carrying the authenticated
	// subject, for the http protocol behind an authenticating proxy.
	//
	// Trusting a header is safe only when nothing but that proxy can reach
	// this listener, which the sidecar topology guarantees by binding
	// loopback or a unix socket. Set this on a listener reachable from
	// anywhere else and a caller can assert any identity.
	IdentityHeader string `json:"identity_header"`

	// IdleTimeoutSec closes a connection with no traffic. Zero disables it.
	// Interactive sessions idle between keystrokes, so a short value breaks
	// psql; leaving it unset is the safe default.
	IdleTimeoutSec int `json:"idle_timeout_sec"`

	// MaxConns bounds concurrency. Zero is unlimited.
	MaxConns int `json:"max_conns"`

	// Policy overrides the top-level default for this listener.
	//
	// Rules CONCATENATE, this listener's first: every rule type denies and
	// evaluation is first-match-wins, so concatenating is monotonic in the
	// allow/deny outcome and order decides only which name and message get
	// reported. Listener-first lets a lane's specific message beat a generic
	// default for the same statement.
	//
	// OPA and Enforce REPLACE when set. Two decision endpoints cannot merge
	// into one, and a lane that says enforce:false means it.
	Policy *PolicyConfig `json:"policy,omitempty"`

	// Mask overrides the top-level default for this listener.
	//
	// Rules REPLACE rather than concatenate: a rule owns an entity type, and
	// concatenating two lists produces two rewrites competing for one entity
	// with slice order picking the winner. Enabled replaces when set.
	Mask *MaskConfig `json:"mask,omitempty"`

	// HTTP configures what this lane's HTTP codec captures. Only valid on
	// an http lane.
	HTTP *HTTPCodecConfig `json:"http,omitempty"`
}

// TLSConfig configures an upstream TLS connection.
type TLSConfig struct {
	// CAFile is a PEM bundle that verifies the upstream. Empty falls back to
	// the host trust store.
	CAFile string `json:"ca_file"`

	// CertFile and KeyFile enable client certificates (mTLS).
	CertFile string `json:"cert_file"`
	KeyFile  string `json:"key_file"`

	// ServerName overrides SNI when the dial address differs from the
	// certificate's name.
	ServerName string `json:"server_name"`

	// InsecureSkipVerify disables verification. The name is verbose on
	// purpose and startup logs a warning when it is on: a proxy built to
	// inspect sensitive traffic should not silently accept any certificate.
	InsecureSkipVerify bool `json:"insecure_skip_verify"`
}

// PolicyConfig configures enforcement.
type PolicyConfig struct {
	// Rules is the local rule set, evaluated first so a statement the
	// local rules already forbid costs no network round trip.
	Rules []policy.Rule `json:"rules"`

	// OPA, when URL is set, is consulted after the local rules pass.
	OPA *OPAConfig `json:"opa"`

	// Enforce false runs in observe-only mode: the gate inspects and audits
	// everything and denies nothing. Teams run this way for a week before
	// turning enforcement on, and it is the default so a misconfigured rule
	// cannot take production down on first deploy.
	//
	// A pointer, so a listener can distinguish "inherit" from an explicit
	// false: a lane rolling out behind an enforcing default needs to say
	// observe-only, and a zero bool cannot express that.
	Enforce *bool `json:"enforce,omitempty"`
}

// OPAConfig configures the OPA client.
type OPAConfig struct {
	URL        string `json:"url"`
	TimeoutSec int    `json:"timeout_sec"`

	// FailOpen allows the statement when OPA is unreachable. Default false,
	// so a policy engine outage stops traffic instead of silently disabling
	// enforcement.
	FailOpen bool `json:"fail_open"`

	// Gate adds a decision BEFORE the AI analyzer runs, letting the policy
	// answer "is this statement worth a model call" by returning
	// `analyze: true|false` beside its allow/deny.
	//
	// It exists to keep the cost control where the policy is. Without it, a
	// lane whose rules defer to OPA calls the model first and OPA second,
	// so a statement Rego would have refused for free has already been
	// paid for. With it, OPA is consulted twice: once to decide whether to
	// spend, once to decide what the answer means.
	//
	// Both calls hit the same URL and carry `input.phase`, so a policy that
	// ignores the field answers both identically and turning the gate on
	// costs one round trip rather than a rewrite. An undefined gate
	// decision means "no opinion" and allows, because a gate is an
	// optimization over a policy someone already wrote.
	//
	// Only meaningful on a lane that has ai_analysis rules; the config is
	// refused otherwise, since a gate over nothing is a round trip that
	// buys nothing.
	Gate bool `json:"gate"`
}

// client builds an OPA evaluator for one phase of this lane's chain.
func (o *OPAConfig) client(phase policy.Phase) *policy.OPAClient {
	timeout := time.Duration(o.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return &policy.OPAClient{
		URL:      o.URL,
		Timeout:  timeout,
		FailOpen: o.FailOpen,
		Phase:    phase,
	}
}

// enabled reports whether this lane consults OPA at all.
func (o *OPAConfig) enabled() bool { return o != nil && o.URL != "" }

// MaskConfig configures response rewriting.
//
// Rules stay raw JSON for the same reason as Config.PII: the shape belongs to
// whichever detector plugin is wired in, and this package must not link one.
// The plugin decodes them.
type MaskConfig struct {
	// Enabled is a pointer for the same reason as PolicyConfig.Enforce: a
	// listener must be able to say "off" against an enabled default, and a
	// zero bool reads as "inherit".
	Enabled *bool           `json:"enabled,omitempty"`
	Rules   json.RawMessage `json:"rules,omitempty"`
}

// on reports whether masking is switched on, treating an absent Enabled as
// off. Rules alone do not enable it: a config listing rules without enabling
// them wants them dormant.
func (m MaskConfig) on() bool { return m.Enabled != nil && *m.Enabled }

// AuditConfig configures the event sink.
type AuditConfig struct {
	// File receives JSON lines. "-" means stdout, which a container
	// deployment wants so the platform's log pipeline collects it.
	File string `json:"file"`

	// RedactStatements replaces statement text with a stable fingerprint,
	// for shops that cannot store query text because literals embed PII but
	// still need to correlate repeated statements.
	RedactStatements bool `json:"redact_statements"`

	// MaxStatementBytes truncates recorded statements. Default 8192.
	MaxStatementBytes int `json:"max_statement_bytes"`

	// AsyncQueueSize wraps the sink in a bounded async queue so a slow disk
	// does not block a user's query. Zero writes synchronously.
	AsyncQueueSize int `json:"async_queue_size"`

	// MemoryBuffer keeps the last N events readable from the admin endpoint.
	// Zero disables it.
	MemoryBuffer int `json:"memory_buffer"`

	// QuerySessions enables the queryable in-memory store behind the admin
	// query API (/api/sessions, /api/events, /api/stats), the endpoints a UI
	// renders. The value bounds how many sessions are retained; when full the
	// oldest session and its whole timeline are evicted, and the API reports
	// the drop count so a reader can tell the window is partial.
	//
	// In-memory by design. A durable, queryable backend lives in the nested
	// module github.com/hoophq/hoop/hoopinspect/store/sqlite, which this binary
	// does NOT import: linking a database driver here would give the sidecar
	// a database driver to audit, and a thin sidecar is an auditable one. A
	// deployment that wants durable queries embeds the library and supplies
	// the SQLite store itself.
	//
	// Zero disables the query API; the JSONL file remains the record of truth
	// either way.
	QuerySessions int `json:"query_sessions"`

	// FailClosed denies a statement when its audit record cannot be written.
	//
	// Default false, which is the uncomfortable default. Turn this on where
	// the audit trail is a compliance requirement rather than an operational
	// convenience: a sink outage then stops traffic, which is correct for a
	// system that exists to prove who did what.
	FailClosed bool `json:"fail_closed"`
}

// AdminConfig configures the health/stats endpoint.
type AdminConfig struct {
	Listen string `json:"listen"`
}

// LoadConfig reads and validates a config file.
//
// Validation is exhaustive rather than fail-fast: it reports every problem at
// once, so you do not fix a config one error per restart.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return LoadConfigBytes(data)
}

// LoadConfigBytes parses and validates JSON config bytes.
//
// It is exported so config from somewhere other than a file (a ConfigMap, a
// secret manager, or the YAML transcoder in the nested module
// github.com/hoophq/hoop/hoopinspect/config/yaml) gets the same strict decode and
// the same validation as the file path.
func LoadConfigBytes(data []byte) (*Config, error) {
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields() // a typo in a key must not silently disable a control
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// resolve merges a listener's overrides onto the top-level defaults.
//
// Policy rules concatenate with the listener's first; OPA and Enforce replace
// when the listener sets them. Mask replaces wholesale. See the field
// documentation on ListenerConfig for why each field merges the way it does.
//
// Pure: it reads config and returns config, so a test can assert the merge
// without building an evaluator, and /config can render it without side
// effects.
func (c *Config) resolve(lc ListenerConfig) (PolicyConfig, MaskConfig) {
	pc := PolicyConfig{
		Rules:   c.Policy.Rules,
		OPA:     c.Policy.OPA,
		Enforce: c.Policy.Enforce,
	}
	if o := lc.Policy; o != nil {
		if len(o.Rules) > 0 {
			// A fresh slice: appending onto c.Policy.Rules would let one
			// listener's rules land in another's through a shared backing
			// array.
			merged := make([]policy.Rule, 0, len(o.Rules)+len(c.Policy.Rules))
			merged = append(merged, o.Rules...)
			merged = append(merged, c.Policy.Rules...)
			pc.Rules = merged
		}
		if o.OPA != nil {
			pc.OPA = o.OPA
		}
		if o.Enforce != nil {
			pc.Enforce = o.Enforce
		}
	}

	mc := c.Mask
	if o := lc.Mask; o != nil {
		if o.Enabled != nil {
			mc.Enabled = o.Enabled
		}
		if len(o.Rules) > 0 {
			mc.Rules = o.Rules
		}
	}
	return pc, mc
}

// enforcing reports whether a resolved policy denies anything.
func (p PolicyConfig) enforcing() bool { return p.Enforce != nil && *p.Enforce }

// Validate checks the config, returning every problem found.
func (c *Config) Validate() error {
	var problems []string

	if len(c.Listeners) == 0 {
		problems = append(problems, "no listeners configured")
	}

	// The analyzer section is checked here as well as in setupAnalyzer, so a
	// config with a bad lane AND a bad analyzer reports both in one run.
	// Splitting them across phases is the "one error per restart" this
	// package refuses everywhere else. setupAnalyzer keeps its own call for
	// a caller that builds a Config by hand and never passes through here.
	problems = append(problems, c.Analyzer.validate(len(c.PII) > 0)...)
	seen := map[string]bool{}
	for i, l := range c.Listeners {
		name := l.displayName(i)
		if l.Protocol == "" {
			problems = append(problems, name+": no protocol")
		} else if _, err := hoopinspect.New(hoopinspect.Protocol(l.Protocol)); err != nil {
			problems = append(problems, fmt.Sprintf("%s: unsupported protocol %q", name, l.Protocol))
		}
		if l.Listen == "" {
			problems = append(problems, name+": no listen address")
		}
		if l.Upstream == "" {
			problems = append(problems, name+": no upstream")
		}
		if l.Network != "" && l.Network != "tcp" && l.Network != "unix" {
			problems = append(problems, fmt.Sprintf("%s: network must be tcp or unix, got %q", name, l.Network))
		}
		key := l.Network + "|" + l.Listen
		if seen[key] {
			problems = append(problems, fmt.Sprintf("%s: duplicate listen address %q", name, l.Listen))
		}
		seen[key] = true

		// downstream_tls is refused at startup rather than accepted and
		// ignored. On any other protocol the relay would never look at the
		// SSLRequest that makes it work, so the lane would come up "green"
		// presenting a certificate nothing ever offers.
		if l.DownstreamTLS != nil {
			if l.Protocol != string(hoopinspect.Postgres) {
				problems = append(problems, fmt.Sprintf(
					"%s: downstream_tls is only supported on postgres, not %q "+
						"(pgwire negotiates TLS in-band, which is the only reason "+
						"the relay terminates it at all)", name, l.Protocol))
			}
			// Load the keypair now. Discovering a bad path on the first
			// client connection means one failed login per restart and
			// nothing in the startup log.
			if _, err := l.DownstreamTLS.BuildDownstreamTLS(); err != nil {
				problems = append(problems, fmt.Sprintf("%s: %v", name, err))
			}
		}

		problems = append(problems, c.validateLane(l, name)...)
	}

	if len(problems) > 0 {
		return fmt.Errorf("invalid config:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return nil
}

// validateLane checks one listener's RESOLVED stack.
//
// Checking the resolved form rather than the two halves separately gives the
// operator "this lane is broken" instead of "some default you inherited
// conflicts with something you set".
func (c *Config) validateLane(lc ListenerConfig, name string) []string {
	var problems []string
	pc, mc := c.resolve(lc)

	// Compile the rules so a bad regex fails at startup rather than on the
	// first request that hits it. With a pii section present, Main runs the
	// real check against the detector; a pii rule has no scanner yet here and
	// would fail for the wrong reason.
	localRules, aiRules := splitAnalyzerRules(pc.Rules)
	if len(localRules) > 0 && len(c.PII) == 0 {
		if _, err := policy.NewRules(localRules); err != nil {
			problems = append(problems, name+": "+err.Error())
		}
	}
	problems = append(problems, validateAIRules(aiRules, c.Analyzer, pc, name)...)
	for _, r := range localRules {
		if r.Action == policy.ActionDefer && !pc.OPA.enabled() {
			// A finding nobody reads is a rule that matches and then
			// allows, which looks like enforcement and is not.
			problems = append(problems, fmt.Sprintf(
				"%s: rule %q defers its match to a policy decision, and the lane has "+
					"no policy.opa.url to defer to; set one or drop the action",
				name, r.Name))
		}
	}
	if pc.OPA != nil && pc.OPA.URL == "" {
		problems = append(problems, name+": policy.opa set but url is empty")
	}

	// An http block on a lane with no HTTP codec would load and do nothing,
	// which is the failure this package refuses everywhere else.
	if lc.HTTP != nil {
		if hoopinspect.Protocol(lc.Protocol) != hoopinspect.HTTP {
			problems = append(problems, fmt.Sprintf(
				"%s: an \"http\" block is only valid on an http listener, not %s",
				name, lc.Protocol))
		}
		problems = append(problems, lc.HTTP.validate(name)...)
	}

	// An ai_analysis rule on an HTTP lane with no body capture classifies
	// nothing: HTTPBuilder.Build returns ok=false on an empty body, and the
	// codec leaves Body empty unless the lane asked for it. The rule would
	// load, evaluate and never fire: the same silent failure the pii-entity
	// check refuses, on a control that also costs money when it does work.
	//
	// This asserts only that the proxy COULD capture a body. A request that
	// carries no body is still skipped at runtime, deliberately: paying for
	// a verdict on "POST /orders" with no payload is what that skip avoids.
	if len(aiRules) > 0 && hoopinspect.Protocol(lc.Protocol) == hoopinspect.HTTP &&
		(lc.HTTP == nil || !lc.HTTP.CaptureBody) {
		problems = append(problems, fmt.Sprintf(
			"%s: has ai_analysis rule(s) on an http listener but http.capture_body "+
				"is not set, so every request would be skipped; add an \"http\" "+
				"block with capture_body: true", name))
	}

	// A lane whose protocol has no content builder classifies nothing, and
	// does so more quietly than any other failure here: Evaluator.classify
	// returns before it has a status, so there is no skipped finding and no
	// annotation, and an operator watching the trail sees a lane behaving
	// exactly as if the rule were absent. That is the mssql case this check
	// exists for, and it covers any protocol that relays without decoding.
	if p := hoopinspect.Protocol(lc.Protocol); len(aiRules) > 0 && p != "" {
		if _, ok := analyzer.BuilderFor(p); !ok {
			problems = append(problems, fmt.Sprintf(
				"%s: has ai_analysis rule(s) on a listener with protocol %q, and this "+
					"build has no content builder for it, so every statement would be "+
					"skipped without leaving a finding", name, p))
		}
	}

	// Mask rule SHAPE belongs to the plugin, which checks it when building
	// the masker. This check covers the one thing knowable here: whether
	// masking can work on this protocol.
	if mc.on() {
		if len(mc.Rules) == 0 {
			problems = append(problems, name+": mask.enabled is true but mask.rules is empty")
		}
		if p := hoopinspect.Protocol(lc.Protocol); p != "" && !gate.MaskSupported(p) {
			problems = append(problems, fmt.Sprintf(
				"%s: mask.enabled is true but masking is not supported on %s "+
					"(its rows are length-prefixed binary frames; rewriting bytes in place "+
					"desynchronizes the client). Set mask.enabled false on this listener.",
				name, lc.Protocol))
		}
	}
	return problems
}

// Plugin is the optional detection engine: it scans statements for the PII
// policy rule, and builds the response masker from the "mask" config section.
//
// It is declared here rather than imported so this package does not link a
// detector. An engine worth having carries recognizers for dozens of national
// identifier formats, and linking one in would give the root module a
// dependency tree, the same reasoning that keeps store/sqlite out (see
// AuditConfig.QuerySessions). The nested module
// github.com/hoophq/hoop/hoopinspect/pii/alcatraz supplies an implementation.
//
// Nil means no detection: pii policy rules are a config error and masking is
// unavailable. Both are refusals, never silent downgrades.
type Plugin interface {
	// ScanText implements policy.Scanner for the pii rule type.
	ScanText(text string) []string

	// Entities lists the entity types this engine looks for. buildLanes uses
	// it to reject a pii rule naming an entity the engine was not configured
	// to detect: that rule would load, evaluate, and never match, silently
	// allowing everything it was written to stop.
	Entities() []string

	// BuildMasker decodes the "mask" config section and returns something
	// the gate can call. rawRules is the JSON array from MaskConfig.Rules;
	// its shape belongs to the plugin.
	//
	// Returning a nil Masker with a nil error means the rules were empty.
	BuildMasker(rawRules []byte) (gate.Masker, error)
}

// buildPolicy assembles one lane's evaluator chain.
//
// The default order is local rules, then OPA, then the AI analyzer, and the
// order is the cost control: Chain short-circuits on the first denial, so a
// statement a free local rule or a Rego rule already refused never reaches a
// paid classifier.
//
// A lane whose rules DEFER to OPA inverts the last two, because a decision
// that reads input.findings has to run after the producers that fill it.
// Adding `opa.gate: true` buys the cost control back by consulting OPA on
// both sides: once to decide whether to spend, once to decide what the
// answer means.
//
// Returns nil in observe-only mode. det may be nil, and a lane with pii rules
// then fails to build by design: a guardrail that cannot see must not
// start.
func buildPolicy(pc PolicyConfig, det Plugin, ac *analyzerDeps) (policy.Evaluator, error) {
	if !pc.enforcing() {
		return nil, nil
	}

	// ai_analysis rules are lifted out before Rules sees them: they need a
	// provider and a deadline, which a local matcher has no business
	// holding, and Rules refuses one that reaches it.
	localRules, aiRules := splitAnalyzerRules(pc.Rules)

	var chain policy.Chain
	if len(localRules) > 0 {
		var rules *policy.Rules
		var err error
		if det != nil {
			rules, err = policy.NewRulesWithScanner(localRules, det)
		} else {
			rules, err = policy.NewRules(localRules)
		}
		if err != nil {
			return nil, err
		}
		chain = append(chain, rules)
	}

	// A lane is two-phase when anything on it defers, whether that is an
	// ai_analysis risk level or a local rule reporting a match. Both need a
	// decision placed AFTER them, because a policy reading input.findings
	// has to run after the thing that fills it.
	twoPhase := pc.OPA.enabled() &&
		(pc.OPA.Gate || anyDeferred(pc.Rules))

	switch {
	case !pc.OPA.enabled():
	case !twoPhase:
		// One decision, no input.findings, exactly as before producers
		// reported. Phase is empty so the input document a single-call
		// lane produces is byte-identical to the old one.
		chain = append(chain, pc.OPA.client(""))
	case pc.OPA.Gate:
		chain = append(chain, pc.OPA.client(policy.PhaseGate))
	}

	if len(aiRules) > 0 {
		var cfg *AnalyzerConfig
		var provider analyzer.Provider
		var redact func(string) string
		if ac != nil {
			cfg, provider, redact = ac.cfg, ac.provider, ac.redact
		}
		evs, err := buildAnalyzerEvaluators(aiRules, cfg, provider, redact)
		if err != nil {
			return nil, err
		}
		for _, ev := range evs {
			chain = append(chain, ev)
		}
	}

	if twoPhase {
		chain = append(chain, pc.OPA.client(policy.PhaseDecide))
	}

	if len(chain) == 0 {
		return nil, nil
	}
	return chain, nil
}

// anyDeferred reports whether any rule hands its match to a later evaluator.
//
// One deferral is enough to make the lane two-phase: the finding has to reach
// something that can act on it, and the only evaluator that can is an OPA
// decision placed after every producer.
//
// It reads the WHOLE rule set, local and ai_analysis alike, because the two
// spell the same request differently: a local rule says `action: defer`, an
// ai_analysis rule says it per risk level.
func anyDeferred(rules []policy.Rule) bool {
	for _, r := range rules {
		if r.Action == policy.ActionDefer {
			return true
		}
		for _, raw := range [...]string{r.HighRisk, r.MediumRisk, r.LowRisk} {
			if analyzer.Action(raw) == analyzer.ActionDefer {
				return true
			}
		}
	}
	return false
}

// analyzerDeps carries the process-wide analyzer to each lane's policy build.
// One provider serves every lane, so the credential is read once.
type analyzerDeps struct {
	cfg      *AnalyzerConfig
	provider analyzer.Provider
	redact   func(string) string
}

// BuildTLS turns a TLSConfig into a *tls.Config.
func (t *TLSConfig) BuildTLS() (*tls.Config, error) {
	if t == nil {
		return nil, nil
	}
	out := &tls.Config{
		ServerName:         t.ServerName,
		InsecureSkipVerify: t.InsecureSkipVerify,
		MinVersion:         tls.VersionTLS12,
	}
	if t.CAFile != "" {
		pem, err := os.ReadFile(t.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read ca_file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("ca_file %q contains no usable certificate", t.CAFile)
		}
		out.RootCAs = pool
	}
	if t.CertFile != "" || t.KeyFile != "" {
		if t.CertFile == "" || t.KeyFile == "" {
			return nil, fmt.Errorf("cert_file and key_file must be set together")
		}
		cert, err := tls.LoadX509KeyPair(t.CertFile, t.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("load client certificate: %w", err)
		}
		out.Certificates = []tls.Certificate{cert}
	}
	return out, nil
}

// BuildDownstreamTLS turns a TLSConfig into a server-side *tls.Config.
//
// Separate from BuildTLS because the two describe opposite ends of a
// connection: BuildTLS produces a CLIENT config, where CAFile verifies a peer
// and ServerName drives SNI. Here the certificate is what the relay PRESENTS,
// so those fields are meaningless and a keypair is mandatory rather than
// optional. Sharing one builder would silently accept a lane configured with
// only a ca_file and then fail every handshake at runtime.
func (t *TLSConfig) BuildDownstreamTLS() (*tls.Config, error) {
	if t == nil {
		return nil, nil
	}
	if t.CertFile == "" || t.KeyFile == "" {
		return nil, fmt.Errorf("downstream_tls needs both cert_file and key_file")
	}
	cert, err := tls.LoadX509KeyPair(t.CertFile, t.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("downstream_tls keypair: %w", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, nil
}
