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
	"github.com/hoophq/hoopinspect/gate"
	"github.com/hoophq/hoopinspect/policy"
)

// Config is the on-disk configuration.
//
// JSON is the native syntax because the module ships zero dependencies and
// the stdlib has no YAML parser. YAML is available through the nested module
// github.com/hoophq/hoopinspect/config/yaml, which transcodes to JSON and
// hands the bytes to LoadConfigBytes — one schema, two syntaxes, and the
// dependency stays out of anything that does not ask for it.
type Config struct {
	// Listeners is the set of protocol endpoints to serve. A sidecar
	// typically runs one, but a per-user pod fronting both a database and an
	// API runs two in one process rather than two containers.
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

	// PII configures the optional detector plugin. It is decoded but not
	// interpreted here: this package must not know what an alcatraz Options
	// looks like, or the dependency it exists to keep out comes back in.
	//
	// The field is declared so DisallowUnknownFields does not reject it, and
	// so a build wired without a detector can say "this section needs one"
	// instead of "unknown field pii".
	PII json.RawMessage `json:"pii,omitempty"`

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

	// Network is "tcp" (default) or "unix". A unix socket is the right
	// choice for a sandbox with no network egress: filesystem permissions,
	// not firewall rules, decide who can reach the proxy.
	Network string `json:"network"`

	// Upstream is the real backend.
	Upstream string `json:"upstream"`

	// Connection is the operator-facing resource name recorded in audit and
	// exposed to policy. This is what a rule and an audit query key on, as
	// distinct from the physical Upstream which may change.
	Connection string `json:"connection"`

	// UpstreamTLS enables TLS to the backend.
	UpstreamTLS *TLSConfig `json:"upstream_tls"`

	// IdentityHeader names an HTTP header carrying the authenticated
	// subject, for the http protocol behind an authenticating proxy.
	//
	// Trusting a header is only safe when nothing can reach this listener
	// except that proxy — which is exactly the sidecar topology, where the
	// listener binds loopback or a unix socket. Setting this on a listener
	// reachable from anywhere else lets a caller assert any identity.
	IdentityHeader string `json:"identity_header"`

	// IdleTimeout closes a connection with no traffic. Zero disables it.
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
	// reported. Listener-first means a lane's specific message beats a
	// generic default for the same statement.
	//
	// OPA and Enforce REPLACE when set. Merging two decision endpoints is
	// meaningless, and a lane that says enforce:false means it.
	Policy *PolicyConfig `json:"policy,omitempty"`

	// Mask overrides the top-level default for this listener.
	//
	// Rules REPLACE rather than concatenate: a rule owns an entity type, and
	// concatenating two lists produces two rewrites competing for one entity
	// with the winner decided by slice order. Enabled replaces when set.
	Mask *MaskConfig `json:"mask,omitempty"`
}

// TLSConfig configures an upstream TLS connection.
type TLSConfig struct {
	// CAFile is a PEM bundle used to verify the upstream. When empty the
	// host trust store is used.
	CAFile string `json:"ca_file"`

	// CertFile and KeyFile enable client certificates (mTLS).
	CertFile string `json:"cert_file"`
	KeyFile  string `json:"key_file"`

	// ServerName overrides SNI when the dial address differs from the
	// certificate's name.
	ServerName string `json:"server_name"`

	// InsecureSkipVerify disables verification. It is deliberately verbose,
	// and startup logs a warning when it is on: a proxy whose entire purpose
	// is inspecting sensitive traffic should not silently accept any
	// certificate.
	InsecureSkipVerify bool `json:"insecure_skip_verify"`
}

// PolicyConfig configures enforcement.
type PolicyConfig struct {
	// Rules is the local rule set, evaluated first so an obviously
	// forbidden statement never costs a network round trip.
	Rules []policy.Rule `json:"rules"`

	// OPA, when URL is set, is consulted after the local rules pass.
	OPA *OPAConfig `json:"opa"`

	// Enforce false runs in observe-only mode: everything is inspected and
	// audited, nothing is denied. This is the mode a team runs for a week
	// before turning enforcement on, and it is the default so a misconfigured
	// rule cannot take production down on first deploy.
	//
	// A pointer so a listener can distinguish "inherit" from an explicit
	// false: a lane rolling out behind an enforcing default needs to say
	// observe-only, and a zero bool cannot express that.
	Enforce *bool `json:"enforce,omitempty"`
}

// OPAConfig configures the OPA client.
type OPAConfig struct {
	URL        string `json:"url"`
	TimeoutSec int    `json:"timeout_sec"`

	// FailOpen allows the statement when OPA is unreachable. Default false:
	// a policy engine outage should stop traffic, not silently disable
	// enforcement.
	FailOpen bool `json:"fail_open"`
}

// MaskConfig configures response rewriting.
//
// Rules are held as raw JSON for the same reason as Config.PII: the shape
// belongs to whichever detector plugin is wired in, and this package must not
// link one. The plugin decodes them.
type MaskConfig struct {
	// Enabled is a pointer for the same reason as PolicyConfig.Enforce: a
	// listener must be able to say "off" against an enabled default, which a
	// zero bool reads as "inherit".
	Enabled *bool           `json:"enabled,omitempty"`
	Rules   json.RawMessage `json:"rules,omitempty"`
}

// on reports whether masking is switched on, treating an absent Enabled as
// off. Rules alone do not enable it: a config that lists rules without
// enabling them is stating an intent to keep them dormant.
func (m MaskConfig) on() bool { return m.Enabled != nil && *m.Enabled }

// AuditConfig configures the event sink.
type AuditConfig struct {
	// File receives JSON lines. "-" means stdout, which is what a container
	// deployment wants so the platform's log pipeline collects it.
	File string `json:"file"`

	// RedactStatements replaces statement text with a stable fingerprint.
	// For shops that cannot store query text because literals embed PII, but
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

	// QuerySessions enables the queryable in-memory store that backs the
	// admin query API (/api/sessions, /api/events, /api/stats) — the
	// endpoints a UI will render. The value bounds how many sessions are
	// retained; when full the oldest session and its whole timeline are
	// evicted, and the API reports the drop count so a reader can tell the
	// window is partial.
	//
	// Deliberately in-memory. A durable, queryable backend lives in the
	// nested module github.com/hoophq/hoopinspect/store/sqlite, which this
	// binary does NOT import: linking a database driver here would give the
	// sidecar a dependency tree, and staying dependency-free is what makes
	// it auditable. A deployment that wants durable queries embeds the
	// library and supplies the SQLite store itself.
	//
	// Zero disables the query API entirely; the JSONL file remains the
	// record of truth either way.
	QuerySessions int `json:"query_sessions"`

	// FailClosed denies a statement when its audit record cannot be written.
	//
	// Default false, and the default is the uncomfortable one. Turn this on
	// where the audit trail is a compliance requirement rather than an
	// operational convenience: then a sink outage stops traffic, which is
	// correct for a system whose purpose is proving who did what.
	FailClosed bool `json:"fail_closed"`
}

// AdminConfig configures the health/stats endpoint.
type AdminConfig struct {
	Listen string `json:"listen"`
}

// LoadConfig reads and validates a config file.
//
// Validation is exhaustive rather than fail-fast: it reports every problem at
// once, because fixing a config one error per restart is miserable.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return LoadConfigBytes(data)
}

// LoadConfigBytes parses and validates JSON config bytes.
//
// Exported so a caller holding config from somewhere other than a file — a
// ConfigMap, a secret manager, or the YAML transcoder in the nested module
// github.com/hoophq/hoopinspect/config/yaml — gets the same strict decode and
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
// Checking the resolved form rather than the two halves separately is the
// point: an operator reads "this lane is broken", not "some default you
// inherited conflicts with something you set".
func (c *Config) validateLane(lc ListenerConfig, name string) []string {
	var problems []string
	pc, mc := c.resolve(lc)

	// Compile the rules so a bad regex fails at startup rather than on the
	// first request that happens to hit it. With a pii section present the
	// real check happens in Main against the actual detector; a pii rule has
	// no scanner yet at this point and would fail for the wrong reason.
	if len(pc.Rules) > 0 && len(c.PII) == 0 {
		if _, err := policy.NewRules(pc.Rules); err != nil {
			problems = append(problems, name+": "+err.Error())
		}
	}
	if pc.OPA != nil && pc.OPA.URL == "" {
		problems = append(problems, name+": policy.opa set but url is empty")
	}

	// Mask rule SHAPE belongs to the plugin and is checked when the masker is
	// built. What can be checked here is whether masking can work at all on
	// this protocol.
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
// a dependency tree — the same reasoning that keeps store/sqlite out (see
// AuditConfig.QuerySessions). The nested module
// github.com/hoophq/hoopinspect/pii/alcatraz supplies an implementation.
//
// Nil means no detection: pii policy rules are a config error and masking is
// unavailable. That is a refusal, never a silent downgrade.
type Plugin interface {
	// ScanText implements policy.Scanner for the pii rule type.
	ScanText(text string) []string

	// Entities lists the entity types this engine will look for. Used to
	// reject a pii rule naming an entity the engine was not configured to
	// detect: the rule would load, evaluate, and never match, which is a
	// guardrail that silently allows everything it was written to stop.
	Entities() []string

	// BuildMasker decodes the "mask" config section and returns something
	// the gate can call. rawRules is the JSON array from MaskConfig.Rules;
	// its shape belongs to the plugin.
	//
	// Returning a nil Masker with a nil error means the rules were empty.
	BuildMasker(rawRules []byte) (gate.Masker, error)
}

// buildPolicy assembles one lane's evaluator chain: local rules first, OPA
// second, so an obviously-forbidden statement never costs a round trip.
//
// Returns nil in observe-only mode. det may be nil; a lane with pii rules then
// fails to build, which is the point — a guardrail that cannot see must not
// start.
func buildPolicy(pc PolicyConfig, det Plugin) (policy.Evaluator, error) {
	if !pc.enforcing() {
		return nil, nil
	}

	var chain policy.Chain
	if len(pc.Rules) > 0 {
		var rules *policy.Rules
		var err error
		if det != nil {
			rules, err = policy.NewRulesWithScanner(pc.Rules, det)
		} else {
			rules, err = policy.NewRules(pc.Rules)
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
	if len(chain) == 0 {
		return nil, nil
	}
	return chain, nil
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
