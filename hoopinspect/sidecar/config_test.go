package sidecar

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoophq/hoopinspect"
	"github.com/hoophq/hoopinspect/policy"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return p
}

// ptr is the three-state-field literal helper: Enforce and Enabled are
// pointers so a listener can say "off" against an enabled default.
//
// Go 1.26's new(expr) would replace this, but the module's go directive is
// 1.24 and this package compiles under that toolchain by design. Revisit when
// the directive moves.
func ptr[T any](v T) *T { return &v }

func TestLoadValidConfig(t *testing.T) {
	p := writeConfig(t, `{
      "listeners": [{
        "name": "appdb",
        "protocol": "postgres",
        "listen": "127.0.0.1:15432",
        "upstream": "db:5432",
        "connection": "appdb"
      }],
      "policy": {
        "enforce": true,
        "rules": [{
          "name": "no-drop",
          "type": "operation",
          "operations": ["drop"],
          "message": "no"
        }]
      },
      "audit": {"file": "-"}
    }`)

	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Listeners) != 1 {
		t.Fatalf("listeners = %d", len(cfg.Listeners))
	}
	if cfg.Listeners[0].Protocol != "postgres" {
		t.Errorf("protocol = %q", cfg.Listeners[0].Protocol)
	}
}

// A typo in a key must not silently disable a control. `enfroce: true` that
// parses as "enforcement off" is the worst possible failure for this system.
func TestUnknownFieldIsRejected(t *testing.T) {
	p := writeConfig(t, `{
      "listeners": [{"protocol":"postgres","listen":":1","upstream":"h:1"}],
      "policy": {"enfroce": true}
    }`)

	if _, err := LoadConfig(p); err == nil {
		t.Fatal("a misspelled key was accepted")
	} else if !strings.Contains(err.Error(), "enfroce") {
		t.Errorf("error does not name the offending key: %v", err)
	}
}

// A bad config should report every problem at once; fixing one error per
// restart is miserable.
func TestValidationReportsEveryProblem(t *testing.T) {
	cfg := &Config{
		Listeners: []ListenerConfig{
			{Name: "a"}, // no protocol, listen, upstream
			{Name: "b", Protocol: "oracle", Listen: ":1"}, // bad protocol, no upstream
		},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("invalid config accepted")
	}
	msg := err.Error()
	for _, want := range []string{"a: no protocol", "a: no listen", "a: no upstream",
		`unsupported protocol "oracle"`, "b: no upstream"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q:\n%s", want, msg)
		}
	}
}

func TestValidationRejectsDuplicateListenAddress(t *testing.T) {
	cfg := &Config{Listeners: []ListenerConfig{
		{Name: "a", Protocol: "postgres", Listen: ":5432", Upstream: "h:1"},
		{Name: "b", Protocol: "mysql", Listen: ":5432", Upstream: "h:2"},
	}}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "duplicate listen address") {
		t.Errorf("duplicate bind accepted: %v", err)
	}
}

func TestValidationRejectsBadNetwork(t *testing.T) {
	cfg := &Config{Listeners: []ListenerConfig{
		{Protocol: "postgres", Listen: ":1", Upstream: "h:1", Network: "udp"},
	}}
	if err := cfg.Validate(); err == nil {
		t.Error("network=udp accepted")
	}
}

// A bad regex must fail at startup, not on the first request that trips it.
func TestBadPolicyRuleFailsAtLoad(t *testing.T) {
	p := writeConfig(t, `{
      "listeners": [{"protocol":"postgres","listen":":1","upstream":"h:1"}],
      "policy": {"rules":[{"name":"r","type":"pattern_match","pattern_regex":"([unclosed"}]}
    }`)

	if _, err := LoadConfig(p); err == nil {
		t.Fatal("an uncompilable regex was accepted at load")
	}
}

// Mask rule SHAPES are no longer validated here — only the plugin knows which
// entity names it detects. What this package must still catch is masking
// switched on with nothing to do, which would otherwise look enabled in the
// config and mask nothing at runtime.
func TestMaskEnabledWithNoRulesFailsAtLoad(t *testing.T) {
	p := writeConfig(t, `{
      "listeners": [{"protocol":"postgres","listen":":1","upstream":"h:1"}],
      "mask": {"enabled": true}
    }`)

	if _, err := LoadConfig(p); err == nil {
		t.Fatal("mask.enabled with no rules was accepted at load")
	}
}

// An unknown entity is the plugin's error, and it must still be fatal — just
// raised where the knowledge lives. Masking enabled with no plugin at all is
// a refusal, never a silent pass-through.
func TestMaskWithoutPluginIsRefused(t *testing.T) {
	mc := MaskConfig{
		Enabled: ptr(true),
		Rules:   []byte(`[{"name":"r","entity":"US_SSN","strategy":"redact"}]`),
	}

	m, err := buildMasker(mc, nil, hoopinspect.HTTP)
	if err == nil {
		t.Fatal("masking without a plugin must fail, not forward responses unmasked")
	}
	if m != nil {
		t.Error("no masker should be returned on error")
	}
}

// Observe-only is the default so a misconfigured rule cannot take production
// down on first deploy.
func TestEnforceDefaultsOff(t *testing.T) {
	cfg := &Config{
		Listeners: []ListenerConfig{{Protocol: "postgres", Listen: ":1", Upstream: "h:1"}},
		Policy: PolicyConfig{
			Rules: []policy.Rule{{
				Name: "no-drop", Type: policy.MatchOperation,
				Operations: []hoopinspect.Operation{hoopinspect.OpDrop},
			}},
		},
	}
	pc, _ := cfg.resolve(cfg.Listeners[0])
	pol, err := buildPolicy(pc, nil)
	if err != nil {
		t.Fatalf("buildPolicy: %v", err)
	}
	if pol != nil {
		t.Error("rules were active with enforce=false — observe-only must not deny")
	}
}

func TestBuildPolicyChainsLocalRulesThenOPA(t *testing.T) {
	pc := PolicyConfig{
		Enforce: ptr(true),
		Rules: []policy.Rule{{
			Name: "no-drop", Type: policy.MatchOperation,
			Operations: []hoopinspect.Operation{hoopinspect.OpDrop},
		}},
		OPA: &OPAConfig{URL: "http://opa:8181/v1/data/hoop"},
	}
	pol, err := buildPolicy(pc, nil)
	if err != nil {
		t.Fatalf("buildPolicy: %v", err)
	}
	chain, ok := pol.(policy.Chain)
	if !ok {
		t.Fatalf("buildPolicy returned %T, want policy.Chain", pol)
	}
	if len(chain) != 2 {
		t.Fatalf("chain length = %d, want 2 (local rules then OPA)", len(chain))
	}
	// Order matters: an obviously forbidden statement must not cost a
	// network round trip.
	if _, isRules := chain[0].(*policy.Rules); !isRules {
		t.Errorf("chain[0] = %T, want *policy.Rules first", chain[0])
	}
	if _, isOPA := chain[1].(*policy.OPAClient); !isOPA {
		t.Errorf("chain[1] = %T, want *policy.OPAClient second", chain[1])
	}
}

func TestOPAWithoutURLIsRejected(t *testing.T) {
	cfg := &Config{
		Listeners: []ListenerConfig{{Protocol: "postgres", Listen: ":1", Upstream: "h:1"}},
		Policy:    PolicyConfig{OPA: &OPAConfig{}},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("an OPA block with no URL was accepted")
	}
}

func TestBuildTLS(t *testing.T) {
	t.Run("nil is nil", func(t *testing.T) {
		var c *TLSConfig
		got, err := c.BuildTLS()
		if err != nil || got != nil {
			t.Errorf("BuildTLS() = (%v, %v), want (nil, nil)", got, err)
		}
	})

	t.Run("minimum version is pinned", func(t *testing.T) {
		got, err := (&TLSConfig{}).BuildTLS()
		if err != nil {
			t.Fatalf("BuildTLS: %v", err)
		}
		if got.MinVersion < 0x0303 { // TLS 1.2
			t.Errorf("MinVersion = %#x, want at least TLS 1.2", got.MinVersion)
		}
	})

	t.Run("cert without key is rejected", func(t *testing.T) {
		if _, err := (&TLSConfig{CertFile: "c.pem"}).BuildTLS(); err == nil {
			t.Error("cert_file without key_file accepted")
		}
	})

	t.Run("unreadable ca fails", func(t *testing.T) {
		if _, err := (&TLSConfig{CAFile: "/nonexistent/ca.pem"}).BuildTLS(); err == nil {
			t.Error("a missing ca_file was accepted")
		}
	})

	t.Run("garbage ca fails", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "ca.pem")
		os.WriteFile(p, []byte("not a certificate"), 0o600)
		if _, err := (&TLSConfig{CAFile: p}).BuildTLS(); err == nil {
			t.Error("a ca_file with no certificate was accepted")
		}
	})
}

func TestDisplayNameFallback(t *testing.T) {
	if got := (ListenerConfig{Name: "n", Connection: "c"}).displayName(0); got != "n" {
		t.Errorf("displayName = %q, want n", got)
	}
	if got := (ListenerConfig{Connection: "c"}).displayName(0); got != "c" {
		t.Errorf("displayName = %q, want c", got)
	}
	if got := (ListenerConfig{}).displayName(3); got != "listener[3]" {
		t.Errorf("displayName = %q", got)
	}
}

func TestMissingConfigFile(t *testing.T) {
	if _, err := LoadConfig("/nonexistent/config.json"); err == nil {
		t.Error("a missing config file was accepted")
	}
}

func TestEmptyListenersRejected(t *testing.T) {
	p := writeConfig(t, `{"listeners": []}`)
	if _, err := LoadConfig(p); err == nil {
		t.Error("a config with no listeners was accepted")
	}
}
