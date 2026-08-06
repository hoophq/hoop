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

	"github.com/hoophq/hoopinspect"
	"github.com/hoophq/hoopinspect/analyzer"
	"github.com/hoophq/hoopinspect/gate"
	"github.com/hoophq/hoopinspect/policy"
)

// Config is the on-disk configuration.
//
// JSON is the native syntax because the module ships zero dependencies and
// the stdlib has no YAML parser. YAML arrives through the nested module
// github.com/hoophq/hoopinspect/config/yaml, which transcodes to JSON and
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

	// Protocol selects the codec: postgres or http.
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
}

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
	// module github.com/hoophq/hoopinspect/store/sqlite, which this binary
	// does NOT import: linking a database driver here would give the sidecar
	// a dependency tree, and a dependency-free sidecar is an auditable one. A
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
// github.com/hoophq/hoopinspect/config/yaml) gets the same strict decode and
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
	problems = append(problems, validateAIRules(aiRules, c.Analyzer, name)...)
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
// It is declared here rather than imported so this package stays
// dependency-free. An engine worth having carries recognizers for dozens of
// national identifier formats, and linking one in would give the root module
// a dependency tree, the same reasoning that keeps store/sqlite out (see
// AuditConfig.QuerySessions). The nested module
// github.com/hoophq/hoopinspect/pii/alcatraz supplies an implementation.
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

// buildPolicy assembles one lane's evaluator chain: local rules first, OPA
// second, so a statement the local rules forbid costs no round trip.
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
	if pc.OPA != nil && pc.OPA.URL != "" {
		timeout := time.Duration(pc.OPA.TimeoutSec) * time.Second
		if timeout <= 0 {
			timeout = 2 * time.Second
		}
		chain = append(chain, &policy.OPAClient{
			URL:      pc.OPA.URL,
			Timeout:  timeout,
			FailOpen: pc.OPA.FailOpen,
		})
	}

	// The analyzer goes LAST, and the ordering is the cost control: a
	// statement a free local rule or OPA already denied never reaches a
	// paid classifier, because Chain short-circuits on the first denial.
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

	if len(chain) == 0 {
		return nil, nil
	}
	return chain, nil
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
