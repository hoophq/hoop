package daemon

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/license"
	"github.com/hoophq/hoop/sidecar/policy"
)

// Config is the on-disk configuration.
//
// JSON is the native syntax because the stdlib has no YAML parser and the
// module takes no dependency it can avoid. YAML arrives through the nested module
// github.com/hoophq/hoop/sidecar/config/yaml, which transcodes to JSON and
// hands the bytes to LoadConfigBytes. One schema, two syntaxes, and the
// dependency stays out of anything that does not ask for it.
//
// # Two spellings, one meaning
//
// ADR-0011 split the old `policy` section into `guardrails` (Hoop's own rule
// set) and `opa` (the client for someone else's Rego), renamed two fields and
// dropped three. The old keys are still declared here and still work, because
// LoadConfigBytes decodes with DisallowUnknownFields: a key that is not a Go
// struct field fails the whole file, so removing one would break every
// deployed config on upgrade rather than warning about it.
//
// normalize folds the deprecated spellings onto the canonical ones before
// anything else reads the struct. Every field marked DEPRECATED below is
// therefore empty by the time resolve, Validate or buildPolicy sees it.
type Config struct {
	// Listeners is the set of protocol endpoints to serve. A sidecar usually
	// runs one; a per-user pod fronting both a database and an API runs two
	// in one process instead of two containers.
	Listeners []ListenerConfig `json:"listeners"`

	// Guardrails is the DEFAULT rule set and enforcement mode, applied to
	// every listener that does not override it. See
	// ListenerConfig.Guardrails.
	//
	// A pointer so normalize can tell "the operator wrote a guardrails
	// block" from "the operator wrote nothing", which decides whether the
	// deprecated `policy` block conflicts with it or folds into it.
	Guardrails *GuardrailsConfig `json:"guardrails,omitempty"`

	// OPA is the DEFAULT decision endpoint, consulted after the local rules
	// pass on every listener that does not override it.
	//
	// It sits beside Guardrails rather than inside it because the two
	// configure different products. A reader of `guardrails.rules` is
	// reading Hoop's matcher; a reader of `opa.url` is reading a client for
	// a Rego policy someone else maintains, on a service they run.
	OPA *OPAConfig `json:"opa,omitempty"`

	// Mask is the DEFAULT response rewriting, applied to every listener that
	// does not override it. See ListenerConfig.Mask.
	Mask *MaskConfig `json:"mask,omitempty"`

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
	//
	// Omitting it no longer disables detection. The plugin builds a detector
	// over every entity type it supports, and the section narrows that set
	// rather than creating it. Enabling a recognizer is not the same as
	// scanning for it: a masker scans only for the entities its own rules
	// name, and a pii guardrail intersects the scan with the entities its
	// own rule names, so a permissive detector costs nothing until a rule
	// asks it for something.
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

	// License is a path to the document Hoop issued, or the document
	// itself: a value starting with "{" is the document, so moving one
	// between a mounted file and a secret is not also a rename. Lowest
	// precedence of the three sources; ResolveLicense holds the order.
	License string `json:"license,omitempty"`

	// Policy is the DEPRECATED pre-ADR-0011 spelling of Guardrails and OPA
	// combined. normalize empties it.
	Policy *PolicyConfig `json:"policy,omitempty"`

	// Deprecations names every deprecated field this config used, phrased
	// for an operator. It is not a config key: normalize fills it, Main and
	// the CLI print it to stderr, and -strict turns it into a non-zero exit.
	//
	// It lives on the Config rather than travelling as a second return value
	// from LoadConfigBytes because three entry points load a config and all
	// three have to report the same thing.
	Deprecations []string `json:"-"`

	// lic is the VERIFIED license ResolveLicense reached. Setup fills it,
	// UseLicense sets it for a caller assembling a Config in Go. Not a
	// config key: the file names a license and does not carry a verdict.
	// The zero value is missing, so an embedder who skips it keeps the caps.
	lic license.Status
}

// Licensing reports the license this config runs under. The zero value is a
// missing license, so this always has something to say.
func (c *Config) Licensing() license.Status { return c.lic }

// UseLicense verifies a license and adopts it, replacing whatever Setup
// resolved. It is the seam a control-plane license arrives through: the plane
// sends the document Hoop signed, the sidecar checks that signature itself
// and every cap moves with the result.
//
// It takes a REFERENCE and not a Status, so no caller can hand the daemon a
// verdict it reached on its own. Trusting the sender would make the caps a
// matter of who is on the other end of a connection.
func (c *Config) UseLicense(ref license.Ref) error {
	s := license.Load(ref)
	if s.State() == license.StateInvalid {
		return s.Err
	}
	c.lic = s
	return nil
}

// ListenerConfig is one protocol endpoint: one Envoy cluster's worth of
// traffic, with its own enforcement stack.
type ListenerConfig struct {
	// Name identifies the listener in logs, in the audit trail and in the
	// OPA input document as input.context.connection.
	//
	// It is the operator-facing resource name: audit queries key on it and
	// the physical Upstream may change under it. Defaults to listener[i],
	// which is a fallback rather than a name anyone should rely on.
	Name string `json:"name"`

	// Protocol selects the codec: postgres, mysql, mssql or http.
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

	// UpstreamTLS enables TLS to the backend.
	UpstreamTLS *TLSConfig `json:"upstream_tls"`

	// DownstreamTLS lets the relay terminate the CLIENT's TLS on this lane.
	// Requires cert_file and key_file; the other TLSConfig fields describe an
	// outbound connection and are ignored here.
	//
	// Only `postgres` supports it. pgwire negotiates TLS in-band with an
	// 8-byte SSLRequest, so a plain TLS listener in front cannot terminate
	// it. Envoy's own postgres filter can, but it is contrib-only, marked
	// work-in-progress, and gives up permanently the moment a client asks
	// for GSS encryption, which is what psql does by default whenever a
	// Kerberos ticket is present.
	//
	// MySQL negotiates in-band too and is still refused, because the relay
	// does not speak that exchange: the server greets first there, and the
	// client's SSLRequest is a truncated HandshakeResponse41 rather than a
	// self-describing 8-byte packet, so none of negotiateDownstream applies.
	// Accepting the field would bind a certificate nothing ever offers and
	// report the lane healthy.
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

	// Guardrails overrides the top-level default for this listener.
	//
	// Rules CONCATENATE, this listener's first: every rule type denies and
	// evaluation is first-match-wins, so concatenating is monotonic in the
	// allow/deny outcome and order decides only which name and message get
	// reported. Listener-first lets a lane's specific message beat a generic
	// default for the same statement.
	//
	// Mode REPLACES when set. A lane rolling out behind an enforcing default
	// means it when it says observe.
	Guardrails *GuardrailsConfig `json:"guardrails,omitempty"`

	// OPA overrides the top-level default for this listener, and REPLACES
	// rather than merging: two decision endpoints cannot become one.
	//
	// An empty block, `opa: {}`, means this lane consults no OPA even when
	// the top level configures one. Without that spelling a top-level
	// endpoint reaches every lane with no way to opt out, which `mask` has
	// through `rules: []` and `guardrails` has through `mode: observe`.
	OPA *OPAConfig `json:"opa,omitempty"`

	// Mask overrides the top-level default for this listener.
	//
	// Rules REPLACE rather than concatenate: a rule owns an entity type, and
	// concatenating two lists produces two rewrites competing for one entity
	// with slice order picking the winner. An empty list, `rules: []`, is how
	// a lane switches inherited masking off.
	Mask *MaskConfig `json:"mask,omitempty"`

	// HTTP configures what this lane's HTTP codec captures. Only valid on
	// an http lane.
	HTTP *HTTPCodecConfig `json:"http,omitempty"`

	// Connection is the DEPRECATED second name for this lane. normalize
	// folds it onto Name, which now fills the audit key and
	// input.context.connection on its own.
	Connection string `json:"connection,omitempty"`

	// Policy is the DEPRECATED pre-ADR-0011 spelling of Guardrails and OPA
	// combined. normalize empties it.
	Policy *PolicyConfig `json:"policy,omitempty"`
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

// Enforcement modes for GuardrailsConfig.Mode.
const (
	// ModeEnforce denies a statement a rule matched. It is the default,
	// because a relay whose rules are configured and inert is a relay
	// nobody notices is broken.
	ModeEnforce = "enforce"

	// ModeObserve evaluates every rule, denies nothing, and records what
	// each match WOULD have denied on the audit line as
	// policy.AnnotationWouldDeny.
	//
	// It is the rollout mode: run it for a week, count the annotations, fix
	// the rules that fire on legitimate traffic, then switch to enforce. It
	// costs what enforcing costs, because nothing can report what would have
	// been denied without evaluating it: a lane with ai_analysis rules makes
	// model calls in this mode, and one with OPA makes round trips. A lane
	// that wants to cost nothing sets `guardrails: {rules: []}` instead.
	ModeObserve = "observe"
)

// GuardrailsConfig configures Hoop's own rule set.
type GuardrailsConfig struct {
	// Mode is enforce or observe. Empty inherits, and an empty resolved
	// mode enforces.
	//
	// A string rather than the *bool this replaced: "" already expresses
	// "inherit", which a zero bool could not, so the pointer that existed to
	// carry three states is no longer needed. A third mode also has
	// somewhere to go.
	Mode string `json:"mode,omitempty"`

	// Rules is the local rule set, evaluated first so a statement the
	// local rules already forbid costs no network round trip.
	Rules []policy.Rule `json:"rules,omitempty"`
}

// enforcing reports whether a resolved lane denies what its rules match.
// An unset mode enforces.
func (g GuardrailsConfig) enforcing() bool { return g.Mode != ModeObserve }

// observing reports whether a resolved lane runs as a dry run.
func (g GuardrailsConfig) observing() bool { return g.Mode == ModeObserve }

// OPAConfig configures the OPA client.
type OPAConfig struct {
	URL        string `json:"url"`
	TimeoutSec int    `json:"timeout_sec"`

	// FailOpen allows the statement when OPA is unreachable. Default false,
	// so a policy engine outage stops traffic instead of silently disabling
	// enforcement.
	FailOpen bool `json:"fail_open"`

	// Gate adds a decision BEFORE the AI analyzer runs, letting the policy
	// answer "is this statement worth a model call" by returning a
	// `request` map beside its allow/deny.
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

// off reports whether a block is the empty `opa: {}` that switches an
// inherited endpoint off for one lane.
//
// Every field zero is the only reading available: a block naming a timeout or
// a fail_open with no url configures a client that cannot be built, which
// validateLane refuses separately.
func (o *OPAConfig) off() bool {
	return o != nil && o.URL == "" && o.TimeoutSec == 0 && !o.FailOpen && !o.Gate
}

// MaskConfig configures response rewriting.
//
// Rules stay raw JSON for the same reason as Config.PII: the shape belongs to
// whichever detector plugin is wired in, and this package must not link one.
// The plugin decodes them.
type MaskConfig struct {
	// Rules is the plugin-owned rule list. A non-empty list switches masking
	// on; there is no separate enable flag, because a list that is present
	// already says whether it is empty.
	//
	// An empty list is meaningful and distinct from an absent one: `rules:
	// []` on a listener replaces an inherited set with nothing, which is how
	// a lane opts out. json.RawMessage preserves that distinction, since the
	// two bytes of `[]` are not the nil slice.
	Rules json.RawMessage `json:"rules,omitempty"`

	// Enabled is the DEPRECATED masking switch. normalize empties it, and
	// an explicit false empties Rules with it so the lane keeps behaving as
	// its author wrote it.
	Enabled *bool `json:"enabled,omitempty"`
}

// hasRules reports whether this resolved mask section asks for any rewriting.
func (m MaskConfig) hasRules() bool { return len(m.Rules) > 0 && !isEmptyJSONList(m.Rules) }

// isEmptyJSONList reports whether raw is an empty JSON array, ignoring
// whitespace. `rules: []` has to read as "no rules" everywhere the resolved
// config is consumed, while still counting as an override where the merge
// happens.
func isEmptyJSONList(raw json.RawMessage) bool {
	return string(bytes.TrimSpace(raw)) == "[]"
}

// PolicyConfig is the DEPRECATED pre-ADR-0011 enforcement section.
//
// It stays declared, and only declared: normalize maps Rules onto
// guardrails.rules, OPA onto the top-level or per-lane opa block, and Enforce
// onto guardrails.mode, then empties the struct. Nothing downstream reads it.
type PolicyConfig struct {
	Rules   []policy.Rule `json:"rules,omitempty"`
	OPA     *OPAConfig    `json:"opa,omitempty"`
	Enforce *bool         `json:"enforce,omitempty"`
}

// set reports whether an operator wrote anything in this block.
func (p *PolicyConfig) set() bool {
	return p != nil && (len(p.Rules) > 0 || p.OPA != nil || p.Enforce != nil)
}

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
	// module github.com/hoophq/hoop/sidecar/store/sqlite, which this binary
	// does NOT import: linking a database driver here would give the sidecar
	// a database driver to audit, and a thin sidecar is an auditable one. A
	// deployment that wants durable queries embeds the library and supplies
	// the SQLite store itself.
	//
	// Zero disables the query API; the JSONL file remains the record of truth
	// either way.
	QuerySessions int `json:"query_sessions"`

	// FailOpen allows a statement whose audit record could not be written.
	//
	// Default FALSE, so a sink outage stops traffic. A system that exists to
	// prove who did what should not keep serving once it stopped being able
	// to prove it.
	//
	// The spelling matters as much as the default. This field replaced
	// `fail_closed`, which meant the opposite of the identically named
	// fields on `opa` and `analyzer`: three sections used one word for one
	// idea and one of them was negated, so `false` meant opposite things a
	// few lines apart. A pointer, so normalize can tell an explicit value
	// from silence and preserve the behaviour of a config that wrote the old
	// field down.
	FailOpen *bool `json:"fail_open,omitempty"`

	// FailClosed is the DEPRECATED inverse of FailOpen. normalize empties it,
	// inverting the value rather than copying it.
	FailClosed *bool `json:"fail_closed,omitempty"`
}

// failOpen resolves the pointer default. Absent means fail closed.
func (a AuditConfig) failOpen() bool { return a.FailOpen != nil && *a.FailOpen }

// failOnAuditError is what the Gate is configured with.
//
// The inversion lives here rather than as a bare `!` at the call site because
// this field changed polarity along with its name: `fail_closed: false` and
// `fail_open: false` mean opposite things, and a flipped sign would hand every
// lane the reverse of the operator's posture with nothing failing.
func (a AuditConfig) failOnAuditError() bool { return !a.failOpen() }

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
// github.com/hoophq/hoop/sidecar/config/yaml) gets the same strict decode, the
// same deprecation folding and the same validation as the file path.
func LoadConfigBytes(data []byte) (*Config, error) {
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields() // a typo in a key must not silently disable a control
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	// Before Validate, so every check below reads canonical fields and no
	// downstream function has to know a deprecated spelling exists.
	if err := cfg.normalize(); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// normalize folds every deprecated field onto its replacement, filling
// Deprecations as it goes, and returns an error naming every field written in
// both spellings.
//
// One funnel, run once, immediately after the decode. The alternative is a
// compat branch in resolve, in validateLane, in buildPolicy and in
// buildMasker, which is four places to forget when the deprecated fields are
// finally removed.
//
// A field written twice is refused rather than resolved by precedence. This
// decoder already refuses a misspelled key on the grounds that a typo must not
// silently disable a control; picking a winner between two spellings of one
// setting fails the same test, and no operator can predict which one wins.
func (c *Config) normalize() error {
	var conflicts []string
	warn := func(format string, args ...any) {
		c.Deprecations = append(c.Deprecations, fmt.Sprintf(format, args...))
	}
	conflict := func(format string, args ...any) {
		conflicts = append(conflicts, fmt.Sprintf(format, args...))
	}

	// audit.fail_closed -> audit.fail_open, INVERTED.
	//
	// The only polarity flip in the migration, and the reason FailClosed is
	// read rather than pattern-matched: an operator who renamed the key
	// without inverting the value would get the opposite of what they had.
	// Reading the old field means a config that wrote it down keeps its
	// behaviour exactly, and only a config that omitted it picks up the new
	// fail-closed default.
	if c.Audit.FailClosed != nil {
		if c.Audit.FailOpen != nil {
			conflict("audit: set audit.fail_open, not both it and the deprecated audit.fail_closed " +
				"(they are inverses, so writing both cannot be resolved)")
		} else {
			inverted := !*c.Audit.FailClosed
			c.Audit.FailOpen = &inverted
			warn("audit.fail_closed: %t is deprecated; write audit.fail_open: %t "+
				"(the sense is inverted, and omitting it now fails closed)",
				*c.Audit.FailClosed, inverted)
		}
		c.Audit.FailClosed = nil
	}

	// policy -> guardrails + opa, at the top level.
	if c.Policy.set() {
		c.Guardrails, c.OPA = c.foldPolicy("policy", c.Policy, c.Guardrails, c.OPA, warn, conflict)
	}
	c.Policy = nil

	// mask.enabled -> the presence of mask.rules.
	c.Mask = normalizeMask("mask", c.Mask, warn)

	for i := range c.Listeners {
		lc := &c.Listeners[i]
		where := lc.Name
		if where == "" {
			where = lc.Connection
		}
		if where == "" {
			where = fmt.Sprintf("listener[%d]", i)
		}

		// connection -> name.
		if lc.Connection != "" {
			switch {
			case lc.Name == "":
				lc.Name = lc.Connection
				warn("%s: listeners[].connection is deprecated; it now travels as "+
					"listeners[].name, which fills the audit key and "+
					"input.context.connection", where)
			case lc.Name == lc.Connection:
				warn("%s: listeners[].connection is deprecated and duplicates name; drop it", where)
			default:
				warn("%s: listeners[].connection %q is deprecated and differs from name %q; "+
					"audit rows and input.context.connection will carry %q. Rename the "+
					"listener to %q to keep the old value",
					where, lc.Connection, lc.Name, lc.Name, lc.Connection)
			}
			lc.Connection = ""
		}

		if lc.Policy.set() {
			lc.Guardrails, lc.OPA = c.foldPolicy(where+": policy", lc.Policy,
				lc.Guardrails, lc.OPA, warn, conflict)
		}
		lc.Policy = nil

		lc.Mask = normalizeMask(where+": mask", lc.Mask, warn)
	}

	if len(conflicts) > 0 {
		return fmt.Errorf("invalid config:\n  - %s", strings.Join(conflicts, "\n  - "))
	}
	return nil
}

// foldPolicy maps one deprecated policy block onto a guardrails block and an
// opa block, reporting a conflict for every field written in both spellings.
//
// Shared by the top level and each listener because the three fields fold the
// same way at both scopes, and two copies would drift the moment one grows a
// case the other does not.
func (c *Config) foldPolicy(
	where string,
	old *PolicyConfig,
	gc *GuardrailsConfig,
	opa *OPAConfig,
	warn, conflict func(string, ...any),
) (*GuardrailsConfig, *OPAConfig) {
	if gc == nil {
		gc = &GuardrailsConfig{}
	}

	if len(old.Rules) > 0 {
		if len(gc.Rules) > 0 {
			conflict("%s.rules is deprecated and guardrails.rules is also set at this scope; "+
				"keep guardrails.rules", where)
		} else {
			gc.Rules = old.Rules
			warn("%s.rules is deprecated; rename it to guardrails.rules", where)
		}
	}

	if old.Enforce != nil {
		mode := ModeEnforce
		if !*old.Enforce {
			mode = ModeObserve
		}
		switch {
		case gc.Mode != "":
			conflict("%s.enforce is deprecated and guardrails.mode is also set at this scope; "+
				"keep guardrails.mode", where)
		default:
			gc.Mode = mode
			warn("%s.enforce: %t is deprecated; write guardrails.mode: %s",
				where, *old.Enforce, mode)
		}
	}

	if old.OPA != nil {
		if opa != nil {
			conflict("%s.opa is deprecated and a sibling opa block is also set at this scope; "+
				"keep the opa block", where)
		} else {
			opa = old.OPA
			warn("%s.opa is deprecated; move it to a sibling opa block", where)
		}
	}

	if gc.Mode == "" && len(gc.Rules) == 0 {
		return nil, opa
	}
	return gc, opa
}

// normalizeMask folds mask.enabled onto the presence of mask.rules.
//
// An explicit false stays authoritative rather than becoming a warning alone:
// a lane written as `enabled: false` with rules listed is masking nothing
// today, and a migration that switched masking ON for it would change what
// leaves the process. Emptying the rule list preserves the behaviour and the
// warning tells the operator how to write it.
func normalizeMask(where string, mc *MaskConfig, warn func(string, ...any)) *MaskConfig {
	if mc == nil || mc.Enabled == nil {
		return mc
	}
	switch {
	case *mc.Enabled:
		warn("%s.enabled: true is deprecated; a non-empty mask.rules is what switches "+
			"masking on, so drop the field", where)
		if len(mc.Rules) == 0 {
			warn("%s.enabled was true with no rules at this scope, which now masks nothing; "+
				"add rules or drop the section", where)
		}
	default:
		warn("%s.enabled: false is deprecated; masking stays off for this scope, and the "+
			"equivalent spelling is mask: {rules: []}", where)
		mc.Rules = json.RawMessage("[]")
	}
	mc.Enabled = nil
	return mc
}

// resolve merges a listener's overrides onto the top-level defaults.
//
// Guardrail rules concatenate with the listener's first; mode, OPA and mask
// replace when the listener sets them. See the field documentation on
// ListenerConfig for why each field merges the way it does.
//
// Pure: it reads config and returns config, so a test can assert the merge
// without building an evaluator, and /config can render it without side
// effects.
func (c *Config) resolve(lc ListenerConfig) (GuardrailsConfig, *OPAConfig, MaskConfig) {
	var gc GuardrailsConfig
	if c.Guardrails != nil {
		gc = *c.Guardrails
	}
	opa := c.OPA

	if o := lc.Guardrails; o != nil {
		// Presence rather than length, because an explicitly empty list is
		// how a lane runs no guardrails at all against a top-level set.
		// Reading `rules: []` as "adds nothing" would be defensible for a
		// field that concatenates, and it would also leave the lane with no
		// way to say it. `opa: {}` and `mask: {rules: []}` spell the same
		// intent for the other two sections; a decoder distinguishes the
		// empty list from an absent key, so all three can mean it.
		switch {
		case o.Rules == nil:
		case len(o.Rules) == 0:
			gc.Rules = nil
		default:
			// A fresh slice: appending onto the default would let one
			// listener's rules land in another's through a shared backing
			// array.
			merged := make([]policy.Rule, 0, len(o.Rules)+len(gc.Rules))
			merged = append(merged, o.Rules...)
			merged = append(merged, gc.Rules...)
			gc.Rules = merged
		}
		if o.Mode != "" {
			gc.Mode = o.Mode
		}
	}
	if o := lc.OPA; o != nil {
		// An empty block switches an inherited endpoint off rather than
		// replacing it with an unbuildable one.
		if o.off() {
			opa = nil
		} else {
			opa = o
		}
	}

	var mc MaskConfig
	if c.Mask != nil {
		mc = *c.Mask
	}
	if o := lc.Mask; o != nil && len(o.Rules) > 0 {
		// Length rather than emptiness: `rules: []` is an override that
		// resolves to nothing, and treating it as silence would leave a lane
		// unable to switch inherited masking off.
		mc.Rules = o.Rules
	}
	return gc, opa, mc
}

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
	//
	// Detection is always available now, so the analyzer's redacting send
	// modes always have a scanner to use.
	problems = append(problems, c.Analyzer.validate(true)...)

	// The feature caps are NOT checked here. This runs inside
	// LoadConfigBytes, before Setup has seen the license flag or
	// HOOP_LICENSE, so a cap here would refuse a licensed config for a
	// limit its license lifts. buildLanes is the single site instead.

	seen := map[string]bool{}
	for i, l := range c.Listeners {
		name := l.displayName(i)
		if l.Protocol == "" {
			problems = append(problems, name+": no protocol")
		} else if _, err := inspect.New(inspect.Protocol(l.Protocol)); err != nil {
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
			if l.Protocol != string(inspect.Postgres) {
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
	gc, opa, mc := c.resolve(lc)

	switch gc.Mode {
	case "", ModeEnforce, ModeObserve:
	default:
		problems = append(problems, fmt.Sprintf(
			"%s: unknown guardrails.mode %q (%s or %s)", name, gc.Mode, ModeEnforce, ModeObserve))
	}

	localRules, aiRules := splitAnalyzerRules(gc.Rules)
	problems = append(problems, validateAIRules(aiRules, c.Analyzer, opa, name)...)

	if opa != nil && opa.URL == "" && !opa.off() {
		problems = append(problems, name+
			": opa is set but url is empty; write an url, or an empty opa: {} to switch "+
			"an inherited endpoint off for this lane")
	}

	// An http block on a lane with no HTTP codec would load and do nothing,
	// which is the failure this package refuses everywhere else.
	if lc.HTTP != nil {
		if inspect.Protocol(lc.Protocol) != inspect.HTTP {
			problems = append(problems, fmt.Sprintf(
				"%s: an \"http\" block is only valid on an http listener, not %s",
				name, lc.Protocol))
		}
		problems = append(problems, lc.HTTP.validate(name)...)
	}

	// An ai_analysis rule on an HTTP lane with no body capture classifies
	// nothing: HTTPBuilder.Build returns ok=false on an empty body, and the
	// codec leaves Body empty unless the lane asked for it. The rule would
	// load, evaluate and never fire: the same silent failure the config
	// refuses everywhere else, on a control that also costs money when it
	// does work.
	//
	// This asserts only that the proxy COULD capture a body. A request that
	// carries no body is still skipped at runtime, deliberately: paying for
	// a verdict on "POST /orders" with no payload is what that skip avoids.
	if len(aiRules) > 0 && inspect.Protocol(lc.Protocol) == inspect.HTTP &&
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
	if p := inspect.Protocol(lc.Protocol); len(aiRules) > 0 && p != "" {
		if _, ok := analyzer.BuilderFor(p); !ok {
			problems = append(problems, fmt.Sprintf(
				"%s: has ai_analysis rule(s) on a listener with protocol %q, and this "+
					"build has no content builder for it, so every statement would be "+
					"skipped without leaving a finding", name, p))
		}
	}

	// Local rule compilation is checked at build time against the real
	// scanner, not here: a pii rule needs one, and NewRules without it fails
	// for the wrong reason. Rules with no pii type can still be compiled
	// early, which catches a bad regex before a port is bound.
	if len(localRules) > 0 && !anyPII(localRules) {
		if _, err := policy.NewRules(localRules); err != nil {
			problems = append(problems, name+": "+err.Error())
		}
	}

	// Mask rule SHAPE belongs to the plugin, which checks it when building
	// the masker. This check covers the one thing knowable here: whether
	// masking can work on this protocol.
	//
	// It runs whenever rules are present, with no enable flag gating it. The
	// flag used to skip this block entirely, so a lane could carry rules for
	// a protocol that cannot mask and still load clean.
	if mc.hasRules() {
		if p := inspect.Protocol(lc.Protocol); p != "" && !gate.MaskSupported(p) {
			problems = append(problems, fmt.Sprintf(
				"%s: masking is not supported on %s (its rows are length-prefixed binary "+
					"frames; rewriting bytes in place desynchronizes the client). Remove "+
					"the rules from this lane, or set mask: {rules: []} on it.",
				name, lc.Protocol))
		}
	}
	return problems
}

// anyPII reports whether a rule set needs a scanner to compile.
func anyPII(rules []policy.Rule) bool {
	for _, r := range rules {
		if r.Type == policy.MatchPII {
			return true
		}
	}
	return false
}

// Plugin is the optional detection engine: it scans statements for the PII
// policy rule, and builds the response masker from the "mask" config section.
//
// It is declared here rather than imported so this package does not link a
// detector. An engine worth having carries recognizers for dozens of national
// identifier formats, and linking one in would give the root module a
// dependency tree, the same reasoning that keeps store/sqlite out (see
// AuditConfig.QuerySessions). The nested module
// github.com/hoophq/hoop/sidecar/pii/alcatraz supplies an implementation.
//
// Nil means this BUILD linked no detector, which is a different thing from an
// absent "pii" config section: the section now narrows a detector that always
// exists. A nil Plugin still makes pii policy rules a config error and masking
// unavailable. Both are refusals, never silent downgrades.
type Plugin interface {
	// ScanText names the entity classes present in a statement. Values never
	// leave the detector: a denial quoting the identifier it denied has
	// published it into the audit trail.
	ScanText(text string) []string

	// Entities lists what this detector was configured to find, for the
	// startup log and for a config error that has to say what is available.
	Entities() []string

	// BuildMasker compiles the raw "mask.rules" JSON into a response
	// rewriter. The rule shape belongs to the plugin.
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
// A lane with NO OPA cannot consume a finding at all, so deferring rules deny
// instead of reporting. `defer` means "hand this to a decision-maker"; with no
// decision-maker the safe reading is refusal, and the alternative of refusing
// the config at startup stopped one file from serving a deployment with OPA
// and a deployment without one.
//
// Returns nil when nothing would evaluate. det may be nil, and a lane with pii
// rules then fails to build by design: a guardrail that cannot see must not
// start.
func buildPolicy(gc GuardrailsConfig, opa *OPAConfig, det Plugin, ac *analyzerDeps) (policy.Evaluator, error) {
	// ai_analysis rules are lifted out before Rules sees them: they need a
	// provider and a deadline, which a local matcher has no business
	// holding, and Rules refuses one that reaches it.
	localRules, aiRules := splitAnalyzerRules(gc.Rules)

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
		// With no OPA there is no evaluator that reads a Finding, so a
		// deferring rule would match, record, and allow.
		rules.DenyDeferred = !opa.enabled()
		chain = append(chain, rules)
	}

	// A lane is two-phase when anything on it defers, whether that is an
	// ai_analysis risk level or a local rule reporting a match. Both need a
	// decision placed AFTER them, because a policy reading input.findings
	// has to run after the thing that fills it.
	twoPhase := opa.enabled() && (opa.Gate || anyDeferred(gc.Rules))

	switch {
	case !opa.enabled():
	case !twoPhase:
		// One decision, no input.findings, exactly as before producers
		// reported. Phase is empty so the input document a single-call
		// lane produces is byte-identical to the old one.
		chain = append(chain, opa.client(""))
	case opa.Gate:
		chain = append(chain, opa.client(policy.PhaseGate))
	}

	if len(aiRules) > 0 {
		var cfg *AnalyzerConfig
		var provider analyzer.Provider
		var redact func(string) string
		if ac != nil {
			cfg, provider, redact = ac.cfg, ac.provider, ac.redact
		}
		evs, err := buildAnalyzerEvaluators(aiRules, cfg, provider, redact, opa.enabled())
		if err != nil {
			return nil, err
		}
		for _, ev := range evs {
			chain = append(chain, ev)
		}
	}

	if twoPhase {
		chain = append(chain, opa.client(policy.PhaseDecide))
	}

	if len(chain) == 0 {
		return nil, nil
	}
	if gc.observing() {
		// Wrapping rather than skipping is what makes observe a dry run.
		// Returning nil here, which is what the mode used to do, left the
		// audit trail unable to say which rule would have refused.
		return policy.Observe{Evaluator: chain}, nil
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
