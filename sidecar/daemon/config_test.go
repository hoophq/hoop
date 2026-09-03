package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return p
}

// hasDeprecation reports whether any notice mentions substr, so a test can
// assert the operator was told which field to change without pinning the
// whole sentence.
func hasDeprecation(cfg *Config, substr string) bool {
	for _, d := range cfg.Deprecations {
		if strings.Contains(d, substr) {
			return true
		}
	}
	return false
}

func TestLoadValidConfig(t *testing.T) {
	p := writeConfig(t, `{
      "listeners": [{
        "name": "appdb",
        "protocol": "postgres",
        "listen": "127.0.0.1:15432",
        "upstream": "db:5432"
      }],
      "guardrails": {
        "mode": "enforce",
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
	if len(cfg.Deprecations) != 0 {
		t.Errorf("a config in the current spelling warned: %v", cfg.Deprecations)
	}
}

// A typo in a key must not silently disable a control. `enfroce: true`
// parsing as "enforcement off" is the worst failure this system can have.
func TestUnknownFieldIsRejected(t *testing.T) {
	p := writeConfig(t, `{
      "listeners": [{"protocol":"postgres","listen":":1","upstream":"h:1"}],
      "guardrails": {"mode": "enforce", "rulez": []}
    }`)

	_, err := LoadConfig(p)
	if err == nil {
		t.Fatal("an unknown field was accepted")
	}
	if !strings.Contains(err.Error(), "rulez") {
		t.Errorf("error does not name the bad key: %v", err)
	}
}

// A bad config reports every problem at once, so nobody fixes one error per
// restart.
func TestValidationReportsEveryProblem(t *testing.T) {
	p := writeConfig(t, `{
      "listeners": [
        {"name":"a","listen":":1","upstream":"h:1"},
        {"name":"b","protocol":"nope","upstream":"h:2","listen":":2"},
        {"name":"c","protocol":"postgres","listen":":3"}
      ]
    }`)

	_, err := LoadConfig(p)
	if err == nil {
		t.Fatal("an invalid config was accepted")
	}
	for _, want := range []string{"a: no protocol", "unsupported protocol", "c: no upstream"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%v", want, err)
		}
	}
}

func TestValidationRejectsDuplicateListenAddress(t *testing.T) {
	p := writeConfig(t, `{
      "listeners": [
        {"name":"a","protocol":"postgres","listen":":1","upstream":"h:1"},
        {"name":"b","protocol":"postgres","listen":":1","upstream":"h:2"}
      ]
    }`)

	if _, err := LoadConfig(p); err == nil {
		t.Error("two listeners on one address were accepted")
	}
}

func TestValidationRejectsBadNetwork(t *testing.T) {
	p := writeConfig(t, `{
      "listeners": [{"protocol":"postgres","listen":":1","upstream":"h:1","network":"udp"}]
    }`)

	if _, err := LoadConfig(p); err == nil {
		t.Error("an unsupported network was accepted")
	}
}

// A bad regex must fail at startup, not on the first request that trips it.
func TestBadGuardrailRuleFailsAtLoad(t *testing.T) {
	p := writeConfig(t, `{
      "listeners": [{"protocol":"postgres","listen":":1","upstream":"h:1"}],
      "guardrails": {"rules":[{"name":"r","type":"pattern_match","pattern_regex":"([unclosed"}]}
    }`)

	if _, err := LoadConfig(p); err == nil {
		t.Fatal("an uncompilable regex was accepted at load")
	}
}

func TestUnknownGuardrailModeIsRejected(t *testing.T) {
	p := writeConfig(t, `{
      "listeners": [{"protocol":"postgres","listen":":1","upstream":"h:1"}],
      "guardrails": {"mode": "audit"}
    }`)

	_, err := LoadConfig(p)
	if err == nil {
		t.Fatal("an unknown mode was accepted")
	}
	if !strings.Contains(err.Error(), "enforce") || !strings.Contains(err.Error(), "observe") {
		t.Errorf("error does not name the valid modes: %v", err)
	}
}

// Masking on a protocol that cannot mask is refused whenever rules are
// present. Under the old mask.enabled flag this check was skipped for a lane
// with the flag off, so a config could carry rules that could never fire and
// still load clean.
//
// Every protocol this build ships masks: gate.MaskSupported asks the codec for
// a Reframer rather than listing names, and postgres, mssql, mysql and http
// all have one. So the refusal is exercised at the buildMasker seam against a
// protocol this build has no codec for, which is where a future non-reframing
// codec would hit it too.
func TestMaskRulesOnUnmaskableProtocolAreRefused(t *testing.T) {
	mc := MaskConfig{Rules: []byte(`[{"entities":["US_SSN"],"strategy":"redact"}]`)}
	det := stubPlugin{entities: []string{"US_SSN"}}

	for _, p := range []inspect.Protocol{inspect.Postgres, inspect.MSSQL, inspect.MySQL, inspect.HTTP} {
		if _, err := buildMasker(mc, det, p); err != nil {
			t.Errorf("buildMasker refused %s, which can re-frame or re-tag: %v", p, err)
		}
	}
	if _, err := buildMasker(mc, det, inspect.Protocol("cassandra")); err == nil {
		t.Error("buildMasker accepted a protocol with neither masking mechanism")
	}
}

// An empty rule list is how a lane opts out of an inherited set, and it has to
// survive the merge: json.RawMessage keeps `[]` distinct from absent, which a
// nil slice could not.
func TestEmptyMaskRulesOptOutOfAnInheritedSet(t *testing.T) {
	cfg := &Config{
		Mask: &MaskConfig{Rules: []byte(`[{"entities":["US_SSN"],"strategy":"redact"}]`)},
		Listeners: []ListenerConfig{
			{Name: "inherits", Protocol: "postgres", Listen: ":1", Upstream: "h:1"},
			{Name: "opts-out", Protocol: "postgres", Listen: ":2", Upstream: "h:2",
				Mask: &MaskConfig{Rules: []byte(`[]`)}},
			{Name: "silent", Protocol: "postgres", Listen: ":3", Upstream: "h:3",
				Mask: &MaskConfig{}},
		},
	}
	if _, _, mc := cfg.resolve(cfg.Listeners[0]); !mc.hasRules() {
		t.Error("the inheriting lane lost the default rule set")
	}
	if _, _, mc := cfg.resolve(cfg.Listeners[1]); mc.hasRules() {
		t.Error("an empty rule list did not switch inherited masking off")
	}
	if _, _, mc := cfg.resolve(cfg.Listeners[2]); !mc.hasRules() {
		t.Error("an empty mask block was read as an opt-out; only rules: [] is")
	}

	lanes, err := buildLanes(cfg, stubPlugin{entities: []string{"US_SSN"}}, nil)
	if err != nil {
		t.Fatalf("buildLanes: %v", err)
	}
	if lanes[0].masker == nil {
		t.Error("the inheriting lane got no masker")
	}
	if lanes[1].masker != nil {
		t.Error("the opted-out lane got a masker")
	}
}

// Masking with no plugin at all is a refusal, never a silent pass-through.
func TestMaskWithoutPluginIsRefused(t *testing.T) {
	mc := MaskConfig{Rules: []byte(`[{"name":"r","entities":["US_SSN"],"strategy":"redact"}]`)}

	m, err := buildMasker(mc, nil, inspect.HTTP)
	if err == nil {
		t.Fatal("masking without a plugin must fail, not forward responses unmasked")
	}
	if m != nil {
		t.Error("no masker should be returned on error")
	}
}

// Enforcement is the default. A relay whose rules are configured and inert is
// a relay nobody notices is broken.
func TestEnforceIsTheDefault(t *testing.T) {
	cfg := &Config{
		Listeners: []ListenerConfig{{Protocol: "postgres", Listen: ":1", Upstream: "h:1"}},
		Guardrails: &GuardrailsConfig{
			Rules: []policy.Rule{{
				Name: "no-drop", Type: policy.MatchOperation,
				Operations: []inspect.Operation{inspect.OpDrop},
			}},
		},
	}
	gc, opa, _ := cfg.resolve(cfg.Listeners[0])
	if !gc.enforcing() {
		t.Fatal("an unset mode did not enforce")
	}
	pol, err := buildPolicy(gc, opa, nil, nil)
	if err != nil {
		t.Fatalf("buildPolicy: %v", err)
	}
	if pol == nil {
		t.Fatal("rules were inert with no mode set; enforce is the default")
	}
	if _, wrapped := pol.(policy.Observe); wrapped {
		t.Error("an enforcing lane was wrapped in the observe dry run")
	}
}

// Observe evaluates every rule and denies nothing, which is the whole
// difference from a lane with no rules: the audit line still names what would
// have been refused.
func TestObserveModeWrapsTheChainInsteadOfSkippingIt(t *testing.T) {
	cfg := &Config{
		Listeners: []ListenerConfig{{Protocol: "postgres", Listen: ":1", Upstream: "h:1"}},
		Guardrails: &GuardrailsConfig{
			Mode: ModeObserve,
			Rules: []policy.Rule{{
				Name: "no-drop", Type: policy.MatchOperation,
				Operations: []inspect.Operation{inspect.OpDrop},
			}},
		},
	}
	gc, opa, _ := cfg.resolve(cfg.Listeners[0])
	pol, err := buildPolicy(gc, opa, nil, nil)
	if err != nil {
		t.Fatalf("buildPolicy: %v", err)
	}
	obs, ok := pol.(policy.Observe)
	if !ok {
		t.Fatalf("buildPolicy returned %T, want policy.Observe", pol)
	}

	v := obs.Evaluate(inspect.Statement{
		Protocol: inspect.Postgres, Operation: inspect.OpDrop, Text: "DROP TABLE t",
	})
	if v.Denied {
		t.Error("observe mode denied a statement")
	}
	if v.Rule != "no-drop" {
		t.Errorf("verdict rule = %q, want the rule that would have denied", v.Rule)
	}
	if v.Annotations[policy.AnnotationWouldDeny] != "no-drop" {
		t.Errorf("annotations = %v, want %s", v.Annotations, policy.AnnotationWouldDeny)
	}
}

// A lane with nothing to enforce relays normally rather than failing.
func TestEnforceWithNoRulesIsAPassThrough(t *testing.T) {
	pol, err := buildPolicy(GuardrailsConfig{Mode: ModeEnforce}, nil, nil, nil)
	if err != nil {
		t.Fatalf("buildPolicy: %v", err)
	}
	if pol != nil {
		t.Error("an empty rule set produced an evaluator")
	}
}

// Observe over an empty chain must stay nil too, or the gate loses its
// short-circuit and every statement walks a wrapper that does nothing.
func TestObserveWithNoRulesStaysNil(t *testing.T) {
	pol, err := buildPolicy(GuardrailsConfig{Mode: ModeObserve}, nil, nil, nil)
	if err != nil {
		t.Fatalf("buildPolicy: %v", err)
	}
	if pol != nil {
		t.Errorf("an empty observe lane produced %T, want nil", pol)
	}
}

func TestBuildPolicyChainsLocalRulesThenOPA(t *testing.T) {
	gc := GuardrailsConfig{
		Rules: []policy.Rule{{
			Name: "no-drop", Type: policy.MatchOperation,
			Operations: []inspect.Operation{inspect.OpDrop},
		}},
	}
	pol, err := buildPolicy(gc, &OPAConfig{URL: "http://opa:8181/v1/data/hoop"}, nil, nil)
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
	// Order matters: a statement the local rules forbid must not cost a
	// network round trip.
	if _, isRules := chain[0].(*policy.Rules); !isRules {
		t.Errorf("chain[0] = %T, want *policy.Rules first", chain[0])
	}
	if _, isOPA := chain[1].(*policy.OPAClient); !isOPA {
		t.Errorf("chain[1] = %T, want *policy.OPAClient second", chain[1])
	}
}

// `defer` names a decision-maker. With no OPA there is none, so the match
// denies rather than recording a finding nobody reads.
func TestDeferDeniesOnALaneWithNoOPA(t *testing.T) {
	gc := GuardrailsConfig{
		Rules: []policy.Rule{{
			Name: "no-drop", Type: policy.MatchOperation,
			Operations: []inspect.Operation{inspect.OpDrop},
			Action:     policy.ActionDefer,
			Message:    "no drops",
		}},
	}
	pol, err := buildPolicy(gc, nil, nil, nil)
	if err != nil {
		t.Fatalf("a deferring rule with no OPA was refused: %v", err)
	}
	v := pol.Evaluate(inspect.Statement{
		Protocol: inspect.Postgres, Operation: inspect.OpDrop, Text: "DROP TABLE t",
	})
	if !v.Denied {
		t.Fatal("a deferring rule allowed on a lane with nothing to defer to")
	}
	if v.Rule != "no-drop" {
		t.Errorf("verdict rule = %q", v.Rule)
	}
}

// The same rule with an OPA endpoint reports instead of denying, which is
// what makes one config file portable across both deployments.
func TestDeferReportsOnALaneWithOPA(t *testing.T) {
	gc := GuardrailsConfig{
		Rules: []policy.Rule{{
			Name: "no-drop", Type: policy.MatchOperation,
			Operations: []inspect.Operation{inspect.OpDrop},
			Action:     policy.ActionDefer,
		}},
	}
	pol, err := buildPolicy(gc, &OPAConfig{URL: "http://opa:8181/v1/data/hoop"}, nil, nil)
	if err != nil {
		t.Fatalf("buildPolicy: %v", err)
	}
	chain := pol.(policy.Chain)
	rules, ok := chain[0].(*policy.Rules)
	if !ok {
		t.Fatalf("chain[0] = %T", chain[0])
	}
	if rules.DenyDeferred {
		t.Error("DenyDeferred was set on a lane that has an OPA consumer")
	}
}

func TestOPAWithoutURLIsRejected(t *testing.T) {
	cfg := &Config{
		Listeners: []ListenerConfig{{Protocol: "postgres", Listen: ":1", Upstream: "h:1"}},
		// A timeout with no url configures a client that cannot be built.
		// An entirely empty block means something else; see below.
		OPA: &OPAConfig{TimeoutSec: 5},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("an OPA block with no URL was accepted")
	}
}

// An empty block is how one lane opts out of an inherited endpoint. Without
// it a top-level OPA reaches every lane with no way to say otherwise.
func TestEmptyOPABlockDisablesAnInheritedEndpoint(t *testing.T) {
	cfg := &Config{
		OPA: &OPAConfig{URL: "http://opa:8181/v1/data/hoop"},
		Listeners: []ListenerConfig{
			{Name: "inherits", Protocol: "postgres", Listen: ":1", Upstream: "h:1"},
			{Name: "opts-out", Protocol: "postgres", Listen: ":2", Upstream: "h:2",
				OPA: &OPAConfig{}},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if _, opa, _ := cfg.resolve(cfg.Listeners[0]); !opa.enabled() {
		t.Error("the inheriting lane lost the top-level endpoint")
	}
	if _, opa, _ := cfg.resolve(cfg.Listeners[1]); opa.enabled() {
		t.Error("an empty opa block did not switch the inherited endpoint off")
	}
}

func TestBuildTLS(t *testing.T) {
	t.Run("nil is nil", func(t *testing.T) {
		var c *TLSConfig
		got, err := c.BuildTLS()
		if err != nil || got != nil {
			t.Errorf("BuildTLS() = %v, %v; want nil, nil", got, err)
		}
	})

	t.Run("cert without key is refused", func(t *testing.T) {
		c := &TLSConfig{CertFile: "/tmp/nope.crt"}
		if _, err := c.BuildTLS(); err == nil {
			t.Error("a certificate with no key was accepted")
		}
	})

	t.Run("insecure_skip_verify survives", func(t *testing.T) {
		c := &TLSConfig{InsecureSkipVerify: true, ServerName: "db.internal"}
		got, err := c.BuildTLS()
		if err != nil {
			t.Fatalf("BuildTLS: %v", err)
		}
		if !got.InsecureSkipVerify {
			t.Error("insecure_skip_verify was dropped")
		}
		if got.ServerName != "db.internal" {
			t.Errorf("ServerName = %q", got.ServerName)
		}
	})

	t.Run("missing ca_file is an error", func(t *testing.T) {
		c := &TLSConfig{CAFile: "/nonexistent/ca.pem"}
		if _, err := c.BuildTLS(); err == nil {
			t.Error("a missing ca_file was accepted")
		}
	})
}

func TestDisplayNameFallback(t *testing.T) {
	if got := (ListenerConfig{Name: "n"}).displayName(0); got != "n" {
		t.Errorf("displayName = %q, want n", got)
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

// A lane that wants to cost nothing needs a way to say so. Observe still
// evaluates, and evaluating is what costs money on a lane with ai_analysis
// rules, so an explicit empty list is the only spelling left.
func TestEmptyGuardrailRulesOptOutOfAnInheritedSet(t *testing.T) {
	cfg := &Config{
		Guardrails: &GuardrailsConfig{Rules: []policy.Rule{{
			Name: "no-drop", Type: policy.MatchOperation,
			Operations: []inspect.Operation{inspect.OpDrop},
		}}},
		Listeners: []ListenerConfig{
			{Name: "inherits", Protocol: "postgres", Listen: ":1", Upstream: "h:1"},
			{Name: "opts-out", Protocol: "postgres", Listen: ":2", Upstream: "h:2",
				Guardrails: &GuardrailsConfig{Rules: []policy.Rule{}}},
			{Name: "silent", Protocol: "postgres", Listen: ":3", Upstream: "h:3",
				Guardrails: &GuardrailsConfig{Mode: ModeEnforce}},
		},
	}
	if gc, _, _ := cfg.resolve(cfg.Listeners[0]); len(gc.Rules) != 1 {
		t.Errorf("the inheriting lane resolved %d rules, want 1", len(gc.Rules))
	}
	if gc, _, _ := cfg.resolve(cfg.Listeners[1]); len(gc.Rules) != 0 {
		t.Errorf("an empty rule list did not opt out: %d rules", len(gc.Rules))
	}
	if gc, _, _ := cfg.resolve(cfg.Listeners[2]); len(gc.Rules) != 1 {
		t.Errorf("a guardrails block with no rules key opted out; only rules: [] does")
	}

	lanes, err := buildLanes(cfg, nil, nil)
	if err != nil {
		t.Fatalf("buildLanes: %v", err)
	}
	if lanes[0].policy == nil {
		t.Error("the inheriting lane got no evaluator")
	}
	if lanes[1].policy != nil {
		t.Error("the opted-out lane still evaluates, so it still costs what it costs")
	}
}

// The empty list has to survive a real decode, which is the only path an
// operator uses. A nil slice and an empty one are the same length and the
// merge reads presence, so this is the assertion that keeps them apart.
func TestEmptyGuardrailRulesSurviveDecoding(t *testing.T) {
	p := writeConfig(t, `{
      "guardrails": {"rules":[{"name":"g","type":"operation","operations":["drop"]}]},
      "listeners": [
        {"name":"a","protocol":"postgres","listen":":1","upstream":"h:1",
         "guardrails":{"rules":[]}},
        {"name":"b","protocol":"postgres","listen":":2","upstream":"h:2",
         "guardrails":{"mode":"enforce"}}
      ]
    }`)
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if gc, _, _ := cfg.resolve(cfg.Listeners[0]); len(gc.Rules) != 0 {
		t.Errorf("rules: [] decoded as absent; lane resolved %d rules", len(gc.Rules))
	}
	if gc, _, _ := cfg.resolve(cfg.Listeners[1]); len(gc.Rules) != 1 {
		t.Errorf("an absent rules key opted the lane out: %d rules", len(gc.Rules))
	}
}

// The audit posture reaches the Gate through one inversion, and the field it
// inverts changed polarity along with its name. A flipped sign here hands
// every lane the reverse of what its operator asked for.
func TestAuditFailOnErrorIsTheInverseOfFailOpen(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  AuditConfig
		want bool
	}{
		{"omitted denies", AuditConfig{}, true},
		{"fail_open false denies", AuditConfig{FailOpen: new(false)}, true},
		{"fail_open true allows", AuditConfig{FailOpen: new(true)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.failOnAuditError(); got != tc.want {
				t.Errorf("failOnAuditError() = %t, want %t", got, tc.want)
			}
		})
	}
}

// ── the deprecation window ────────────────────────────────────────────────
//
// Every test below feeds a pre-ADR-0011 config. They exist because the
// promise is that a deployed config keeps working, and a promise nothing
// checks is a promise until the first upgrade.

func TestDeprecatedPolicyBlockStillLoads(t *testing.T) {
	p := writeConfig(t, `{
      "listeners": [{
        "name": "appdb", "protocol": "postgres",
        "listen": ":1", "upstream": "h:1", "connection": "appdb"
      }],
      "policy": {
        "enforce": true,
        "rules": [{"name":"no-drop","type":"operation","operations":["drop"]}],
        "opa": {"url": "http://opa:8181/v1/data/hoop"}
      },
      "mask": {"enabled": true, "rules": [{"entity":"US_SSN","strategy":"redact"}]}
    }`)

	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("a pre-0011 config was refused: %v", err)
	}
	if cfg.Policy != nil {
		t.Error("normalize left the deprecated policy block populated")
	}
	if cfg.Guardrails == nil || cfg.Guardrails.Mode != ModeEnforce {
		t.Errorf("guardrails = %+v, want mode enforce", cfg.Guardrails)
	}
	if len(cfg.Guardrails.Rules) != 1 {
		t.Errorf("rules = %d, want the policy rule folded across", len(cfg.Guardrails.Rules))
	}
	if !cfg.OPA.enabled() {
		t.Error("policy.opa did not fold onto the top-level opa block")
	}
	if cfg.Listeners[0].Connection != "" {
		t.Error("normalize left the deprecated connection field populated")
	}
	if cfg.Mask == nil || cfg.Mask.Enabled != nil {
		t.Errorf("mask = %+v, want enabled folded away", cfg.Mask)
	}
	for _, want := range []string{"policy.rules", "policy.enforce", "policy.opa",
		"connection", "mask.enabled"} {
		if !hasDeprecation(cfg, want) {
			t.Errorf("no deprecation notice mentions %q; got %v", want, cfg.Deprecations)
		}
	}
}

// enforce: false is what observe-only used to be spelled.
func TestDeprecatedEnforceFalseBecomesObserve(t *testing.T) {
	p := writeConfig(t, `{
      "listeners": [{"protocol":"postgres","listen":":1","upstream":"h:1"}],
      "policy": {"enforce": false, "rules":[{"name":"r","type":"operation","operations":["drop"]}]}
    }`)

	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Guardrails.Mode != ModeObserve {
		t.Errorf("mode = %q, want %q", cfg.Guardrails.Mode, ModeObserve)
	}
}

// A lane that named only `connection` must keep that name, or its audit
// history splits across two keys on upgrade.
func TestDeprecatedConnectionBecomesTheName(t *testing.T) {
	p := writeConfig(t, `{
      "listeners": [{"protocol":"postgres","listen":":1","upstream":"h:1","connection":"metabase"}]
    }`)

	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Listeners[0].Name != "metabase" {
		t.Errorf("name = %q, want the old connection value", cfg.Listeners[0].Name)
	}
}

// A lane written as enabled:false with rules listed masks nothing today. The
// migration must not switch masking ON for it.
func TestDeprecatedMaskEnabledFalseKeepsMaskingOff(t *testing.T) {
	p := writeConfig(t, `{
      "listeners": [{"protocol":"postgres","listen":":1","upstream":"h:1"}],
      "mask": {"enabled": false, "rules": [{"entity":"US_SSN","strategy":"redact"}]}
    }`)

	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	_, _, mc := cfg.resolve(cfg.Listeners[0])
	if mc.hasRules() {
		t.Error("mask.enabled:false was dropped and masking turned itself on")
	}
}

// The polarity inversion. A config that wrote the old field down keeps its
// behaviour exactly; only a config that omitted it picks up the new default.
func TestDeprecatedAuditFailClosedInverts(t *testing.T) {
	for _, tc := range []struct {
		name       string
		body       string
		wantOpen   bool
		wantNotice bool
	}{
		{"explicit false stays open", `"audit":{"fail_closed":false}`, true, true},
		{"explicit true stays closed", `"audit":{"fail_closed":true}`, false, true},
		{"new spelling is honoured", `"audit":{"fail_open":true}`, true, false},
		{"omitted fails closed", `"audit":{"file":"-"}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := writeConfig(t, `{
              "listeners": [{"protocol":"postgres","listen":":1","upstream":"h:1"}],
              `+tc.body+`}`)
			cfg, err := LoadConfig(p)
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if got := cfg.Audit.failOpen(); got != tc.wantOpen {
				t.Errorf("failOpen() = %t, want %t", got, tc.wantOpen)
			}
			if cfg.Audit.FailClosed != nil {
				t.Error("normalize left the deprecated field populated")
			}
			if got := hasDeprecation(cfg, "fail_closed"); got != tc.wantNotice {
				t.Errorf("deprecation notice = %t, want %t (%v)", got, tc.wantNotice, cfg.Deprecations)
			}
		})
	}
}

// Picking a winner between two spellings of one setting fails the same test a
// misspelled key fails: no operator can predict the answer.
func TestBothSpellingsIsRefused(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"rules", `"guardrails":{"rules":[{"name":"a","type":"operation","operations":["drop"]}]},
                   "policy":{"rules":[{"name":"b","type":"operation","operations":["drop"]}]}`},
		{"mode", `"guardrails":{"mode":"observe"},"policy":{"enforce":true}`},
		{"opa", `"opa":{"url":"http://a"},"policy":{"opa":{"url":"http://b"}}`},
		{"audit", `"audit":{"fail_open":true,"fail_closed":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := writeConfig(t, `{
              "listeners": [{"protocol":"postgres","listen":":1","upstream":"h:1"}],
              `+tc.body+`}`)
			if _, err := LoadConfig(p); err == nil {
				t.Fatal("a config written in both spellings was accepted")
			}
		})
	}
}

// Every conflict in one run, matching the rule the rest of validation follows.
func TestEveryConflictIsReportedAtOnce(t *testing.T) {
	p := writeConfig(t, `{
      "listeners": [{"protocol":"postgres","listen":":1","upstream":"h:1"}],
      "guardrails": {"mode":"observe","rules":[{"name":"a","type":"operation","operations":["drop"]}]},
      "opa": {"url":"http://a"},
      "policy": {"enforce":true,"opa":{"url":"http://b"},
                 "rules":[{"name":"b","type":"operation","operations":["drop"]}]}
    }`)

	_, err := LoadConfig(p)
	if err == nil {
		t.Fatal("a conflicting config was accepted")
	}
	for _, want := range []string{"policy.rules", "policy.enforce", "policy.opa"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%v", want, err)
		}
	}
}

// The deprecated fields fold at listener scope too, and a listener block is
// where a real migration is most likely to be half-finished.
func TestDeprecatedListenerPolicyFolds(t *testing.T) {
	p := writeConfig(t, `{
      "listeners": [{
        "name":"appdb","protocol":"postgres","listen":":1","upstream":"h:1",
        "policy": {"enforce": false, "opa": {"url":"http://opa:8181/x"},
                   "rules":[{"name":"lane","type":"operation","operations":["drop"]}]}
      }]
    }`)

	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	lc := cfg.Listeners[0]
	if lc.Policy != nil {
		t.Error("normalize left the listener's policy block populated")
	}
	if lc.Guardrails == nil || lc.Guardrails.Mode != ModeObserve || len(lc.Guardrails.Rules) != 1 {
		t.Errorf("listener guardrails = %+v", lc.Guardrails)
	}
	if !lc.OPA.enabled() {
		t.Error("the listener's policy.opa did not fold onto its opa block")
	}
	if !hasDeprecation(cfg, "appdb") {
		t.Errorf("the notice does not name the lane: %v", cfg.Deprecations)
	}
}

// The mask rule shape belongs to the plugin, so the daemon must pass `entity`
// through untouched for the plugin to warn about.
func TestDeprecatedMaskEntityIsLeftToThePlugin(t *testing.T) {
	p := writeConfig(t, `{
      "listeners": [{"protocol":"postgres","listen":":1","upstream":"h:1"}],
      "mask": {"rules": [{"entity":"US_SSN","strategy":"redact"}]}
    }`)

	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	var got []map[string]any
	if err := json.Unmarshal(cfg.Mask.Rules, &got); err != nil {
		t.Fatalf("mask rules: %v", err)
	}
	if got[0]["entity"] != "US_SSN" {
		t.Errorf("the daemon rewrote a plugin-owned rule: %v", got[0])
	}
}
