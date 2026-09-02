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
