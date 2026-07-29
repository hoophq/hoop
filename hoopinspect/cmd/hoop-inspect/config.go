package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/hoophq/hoopinspect"
	"github.com/hoophq/hoopinspect/mask"
	"github.com/hoophq/hoopinspect/policy"
)

// Config is the on-disk configuration. JSON rather than YAML because the
// module ships zero dependencies and the stdlib has no YAML parser; adding
// one would break the auditability claim for a syntax preference.
type Config struct {
	// Listeners is the set of protocol endpoints to serve. A sidecar
	// typically runs one, but a per-user pod fronting both a database and an
	// API runs two in one process rather than two containers.
	Listeners []ListenerConfig `json:"listeners"`

	// Policy configures the local rule set and the optional OPA client.
	Policy PolicyConfig `json:"policy"`

	// Mask configures response rewriting.
	Mask MaskConfig `json:"mask"`

	// Audit configures where events go.
	Audit AuditConfig `json:"audit"`

	// Admin serves health and stats. Disabled when Listen is empty.
	Admin AdminConfig `json:"admin"`

	// LogLevel is debug, info, warn or error. Default info.
	LogLevel string `json:"log_level"`
}

// ListenerConfig is one protocol endpoint.
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
	Enforce bool `json:"enforce"`
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
type MaskConfig struct {
	Enabled bool        `json:"enabled"`
	Rules   []mask.Rule `json:"rules"`
}

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
	var cfg Config
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields() // a typo in a key must not silently disable a control
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate checks the config, returning every problem found.
func (c *Config) Validate() error {
	var problems []string

	if len(c.Listeners) == 0 {
		problems = append(problems, "no listeners configured")
	}
	seen := map[string]bool{}
	for i, l := range c.Listeners {
		name := l.Name
		if name == "" {
			name = fmt.Sprintf("listeners[%d]", i)
		}
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
	}

	// Compile the rules here so a bad regex fails at startup rather than on
	// the first request that happens to hit it.
	if len(c.Policy.Rules) > 0 {
		if _, err := policy.NewRules(c.Policy.Rules); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if c.Mask.Enabled && len(c.Mask.Rules) > 0 {
		if _, err := mask.New(c.Mask.Rules); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if c.Policy.OPA != nil && c.Policy.OPA.URL == "" {
		problems = append(problems, "policy.opa set but url is empty")
	}

	if len(problems) > 0 {
		return fmt.Errorf("invalid config:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return nil
}

// BuildPolicy assembles the evaluator chain: local rules first, OPA second.
// Returns nil in observe-only mode.
func (c *Config) BuildPolicy() (policy.Evaluator, error) {
	if !c.Policy.Enforce {
		return nil, nil
	}

	var chain policy.Chain
	if len(c.Policy.Rules) > 0 {
		rules, err := policy.NewRules(c.Policy.Rules)
		if err != nil {
			return nil, err
		}
		chain = append(chain, rules)
	}
	if c.Policy.OPA != nil && c.Policy.OPA.URL != "" {
		timeout := time.Duration(c.Policy.OPA.TimeoutSec) * time.Second
		if timeout <= 0 {
			timeout = 2 * time.Second
		}
		chain = append(chain, &policy.OPAClient{
			URL:      c.Policy.OPA.URL,
			Timeout:  timeout,
			FailOpen: c.Policy.OPA.FailOpen,
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
