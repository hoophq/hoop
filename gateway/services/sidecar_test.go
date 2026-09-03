package services

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/policy"
)

func b64(v string) string { return base64.StdEncoding.EncodeToString([]byte(v)) }

func pgConn(name, host, port string) sidecarConnection {
	return sidecarConnection{
		name:     name,
		connType: "database",
		subType:  "postgres",
		envs: map[string]string{
			"envvar:HOST": b64(host),
			"envvar:PORT": b64(port),
		},
	}
}

func TestBuildConfigPostgresListener(t *testing.T) {
	conn := pgConn("pg-prod", "db.internal", "5432")
	conn.guardRailInputRules = json.RawMessage(`[{"rules":[{"type":"deny_words_list","words":["DROP"],"message":"no drops"}]}]`)
	conn.dataMaskingRules = json.RawMessage(`[{"name":"contact","supported_entity_types":[{"name":"personal","entity_types":["EMAIL_ADDRESS"]}],"custom_entity_types":[],"score_threshold":0.6}]`)

	cfg, err := buildConfig([]sidecarConnection{conn})
	if err != nil {
		t.Fatalf("buildConfig: %v", err)
	}
	if len(cfg.Listeners) != 1 {
		t.Fatalf("want 1 listener, got %d", len(cfg.Listeners))
	}
	l := cfg.Listeners[0]
	if l.Name != "pg-prod" {
		t.Errorf("name: want pg-prod, got %q", l.Name)
	}
	if l.Protocol != "postgres" {
		t.Errorf("protocol: want postgres, got %q", l.Protocol)
	}
	if l.Upstream != "db.internal:5432" {
		t.Errorf("upstream: want db.internal:5432, got %q", l.Upstream)
	}
	if l.Listen != "0.0.0.0:5432" {
		t.Errorf("listen: want 0.0.0.0:5432, got %q", l.Listen)
	}
	if l.Guardrails == nil || len(l.Guardrails.Rules) != 1 {
		t.Fatalf("want one guardrail rule, got %+v", l.Guardrails)
	}
	rule := l.Guardrails.Rules[0]
	if rule.Type != policy.MatchDenyWords {
		t.Errorf("rule type: want deny_words_list, got %q", rule.Type)
	}
	if rule.Name == "" {
		t.Error("rule name must not be blank: it shows in verdicts and audit rows")
	}
	if len(rule.Words) != 1 || rule.Words[0] != "DROP" {
		t.Errorf("rule words: want [DROP], got %v", rule.Words)
	}
	if l.Mask == nil {
		t.Fatal("want a mask section")
	}
	var maskRules []struct {
		Name     string   `json:"name"`
		Entities []string `json:"entities"`
		Strategy string   `json:"strategy"`
	}
	if err := json.Unmarshal(l.Mask.Rules, &maskRules); err != nil {
		t.Fatalf("decode mask rules: %v", err)
	}
	if len(maskRules) != 1 || len(maskRules[0].Entities) != 1 || maskRules[0].Entities[0] != "EMAIL_ADDRESS" {
		t.Fatalf("mask rules: want one EMAIL_ADDRESS rule, got %+v", maskRules)
	}
	if maskRules[0].Strategy != "redact" {
		t.Errorf("mask strategy: want redact, got %q", maskRules[0].Strategy)
	}

	// The emitted config must survive the sidecar's own loader.
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := daemon.LoadConfigBytes(encoded); err != nil {
		t.Fatalf("the sidecar loader refused the emitted config: %v", err)
	}
}

func TestBuildConfigDuplicateListenAddress(t *testing.T) {
	_, err := buildConfig([]sidecarConnection{
		pgConn("pg-a", "a.internal", "5432"),
		pgConn("pg-b", "b.internal", "5432"),
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate listen address") {
		t.Fatalf("want a duplicate listen address error, got %v", err)
	}
}

func TestBuildConfigRefusesGuardrailOutputRules(t *testing.T) {
	conn := pgConn("pg-prod", "db.internal", "5432")
	conn.guardRailOutputRules = json.RawMessage(`[{"rules":[{"type":"deny_words_list","words":["secret"]}]}]`)

	_, err := buildConfig([]sidecarConnection{conn})
	if err == nil || !strings.Contains(err.Error(), "guardrail output rules") {
		t.Fatalf("want a guardrail output rules error, got %v", err)
	}
}

func TestBuildConfigRefusesCustomMaskingEntities(t *testing.T) {
	conn := pgConn("pg-prod", "db.internal", "5432")
	conn.dataMaskingRules = json.RawMessage(`[{"name":"internal-id","supported_entity_types":[],"custom_entity_types":[{"name":"emp","regex":"E-[0-9]+","deny_list":[],"score":0.8}]}]`)

	_, err := buildConfig([]sidecarConnection{conn})
	if err == nil || !strings.Contains(err.Error(), "custom entity types") {
		t.Fatalf("want a custom entity types error, got %v", err)
	}
}

func TestBuildConfigRefusesUnsupportedProtocol(t *testing.T) {
	conn := pgConn("mysql-prod", "db.internal", "3306")
	conn.subType = "mysql"

	_, err := buildConfig([]sidecarConnection{conn})
	if err == nil || !strings.Contains(err.Error(), "cannot be served by a sidecar") {
		t.Fatalf("want an unsupported protocol error, got %v", err)
	}
}

func httpConn(name, remoteURL string) sidecarConnection {
	return sidecarConnection{
		name:     name,
		connType: "httpproxy",
		subType:  "claude-code",
		envs:     map[string]string{"envvar:REMOTE_URL": b64(remoteURL)},
	}
}

func TestBuildConfigHTTPUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, url, upstream, listen string
		tls                         bool
	}{
		{"explicit port", "https://api.example.com:8443/v1", "api.example.com:8443", "0.0.0.0:8443", true},
		{"https default", "https://api.example.com/v1", "api.example.com:443", "0.0.0.0:443", true},
		{"http default", "http://api.example.com/v1", "api.example.com:80", "0.0.0.0:80", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := buildConfig([]sidecarConnection{httpConn("api", tc.url)})
			if err != nil {
				t.Fatalf("buildConfig: %v", err)
			}
			l := cfg.Listeners[0]
			if l.Protocol != "http" {
				t.Errorf("protocol: want http, got %q", l.Protocol)
			}
			if l.Upstream != tc.upstream {
				t.Errorf("upstream: want %q, got %q", tc.upstream, l.Upstream)
			}
			if l.Listen != tc.listen {
				t.Errorf("listen: want %q, got %q", tc.listen, l.Listen)
			}
			if tc.tls && l.UpstreamTLS == nil {
				t.Errorf("upstream_tls: want enabled for %q, got nil", tc.url)
			}
			if !tc.tls && l.UpstreamTLS != nil {
				t.Errorf("upstream_tls: want nil for %q, got %+v", tc.url, l.UpstreamTLS)
			}
		})
	}
}

// A HOST carrying its own port would build "host:port:port", which
// daemon.Validate accepts (it only checks non-empty) and the sidecar then
// fails to dial at the first client connection.
func TestBuildConfigRefusesHostWithPort(t *testing.T) {
	_, err := buildConfig([]sidecarConnection{pgConn("pg", "db.internal:5432", "5432")})
	if err == nil || !strings.Contains(err.Error(), "already carries a port") {
		t.Fatalf("want a host-with-port error, got %v", err)
	}
}

func TestBuildConfigBracketsIPv6Host(t *testing.T) {
	cfg, err := buildConfig([]sidecarConnection{pgConn("pg", "fd00::1", "5432")})
	if err != nil {
		t.Fatalf("buildConfig: %v", err)
	}
	if got := cfg.Listeners[0].Upstream; got != "[fd00::1]:5432" {
		t.Errorf("upstream: want [fd00::1]:5432, got %q", got)
	}
}

func TestBuildConfigRefusesUnbindablePort(t *testing.T) {
	for _, port := range []string{"0", "-1", "+5432", "99999", "5432 "} {
		if _, err := buildConfig([]sidecarConnection{pgConn("pg", "db.internal", port)}); err == nil {
			t.Errorf("port %q: want an error, got nil", port)
		}
	}
}

// A present-but-corrupt value must not be reported as an absent one, or the
// operator hunts for an env var that is already set.
func TestBuildConfigReportsCorruptEnvDistinctly(t *testing.T) {
	conn := pgConn("pg", "db.internal", "5432")
	conn.envs["envvar:HOST"] = "!!!not-base64!!!"

	_, err := buildConfig([]sidecarConnection{conn})
	if err == nil || !strings.Contains(err.Error(), "unreadable HOST") {
		t.Fatalf("want an unreadable-HOST error, got %v", err)
	}
}

func TestBuildConfigMissingHostAndPort(t *testing.T) {
	conn := pgConn("pg", "db.internal", "5432")
	delete(conn.envs, "envvar:PORT")
	if _, err := buildConfig([]sidecarConnection{conn}); err == nil ||
		!strings.Contains(err.Error(), "no PORT configured") {
		t.Fatalf("want a missing-PORT error, got %v", err)
	}
}

// The PII section is the union of every entity the mask rules name, with
// duplicates removed. Note the cap: this build allows one mask rule per
// process, so a single rule with several entity groups is the only shape
// that reaches the union code.
func TestBuildConfigPIIUnionDeduplicates(t *testing.T) {
	conn := pgConn("pg", "db.internal", "5432")
	conn.dataMaskingRules = json.RawMessage(`[{"name":"pii","supported_entity_types":[` +
		`{"name":"x","entity_types":["EMAIL_ADDRESS"]},` +
		`{"name":"y","entity_types":["US_SSN","EMAIL_ADDRESS"]}],"score_threshold":0.4}]`)

	cfg, err := buildConfig([]sidecarConnection{conn})
	if err != nil {
		t.Fatalf("buildConfig: %v", err)
	}
	var pii struct {
		Entities  []string `json:"entities"`
		Threshold float64  `json:"threshold"`
	}
	if err := json.Unmarshal(cfg.PII, &pii); err != nil {
		t.Fatalf("decode pii: %v", err)
	}
	if pii.Threshold != 0.4 {
		t.Errorf("threshold: want 0.4, got %v", pii.Threshold)
	}
	if len(pii.Entities) != 2 {
		t.Errorf("entities must be de-duplicated, got %v", pii.Entities)
	}
}

// An empty words list is a no-op for the gateway matcher, so it must not
// reach the sidecar as a rule at all.
func TestBuildConfigSkipsEmptyGuardrailEntries(t *testing.T) {
	conn := pgConn("pg", "db.internal", "5432")
	conn.guardRailInputRules = json.RawMessage(`[{"rules":[{"type":"deny_words_list","words":["",""]},{"type":"pattern_match","pattern_regex":""}]}]`)

	cfg, err := buildConfig([]sidecarConnection{conn})
	if err != nil {
		t.Fatalf("buildConfig: %v", err)
	}
	if cfg.Listeners[0].Guardrails != nil {
		t.Errorf("want no guardrails section, got %+v", cfg.Listeners[0].Guardrails)
	}
}
