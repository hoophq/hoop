package sidecar

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/hoophq/hoop/hoopinspect"
	"github.com/hoophq/hoop/hoopinspect/gate"
	"github.com/hoophq/hoop/hoopinspect/policy"
)

// stubPlugin is a detection plugin that finds whatever literal it is told to.
type stubPlugin struct {
	entities []string
	find     string
}

func (s stubPlugin) Entities() []string { return s.entities }

func (s stubPlugin) ScanText(text string) []string {
	if s.find != "" && strings.Contains(text, s.find) {
		return []string{s.entities[0]}
	}
	return nil
}

// BuildMasker mirrors the real plugin: a rule naming an entity it does not
// detect is a config error, so it cannot become a rule that silently never
// fires.
func (s stubPlugin) BuildMasker(raw []byte) (gate.Masker, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var rules []struct {
		Entity string `json:"entity"`
	}
	if err := json.Unmarshal(raw, &rules); err != nil {
		return nil, err
	}
	for _, r := range rules {
		if r.Entity != "" && !slices.Contains(s.entities, r.Entity) {
			return nil, fmt.Errorf("entity %q is not detected", r.Entity)
		}
	}
	return noopMasker{}, nil
}

type noopMasker struct{}

func (noopMasker) Mask(d []byte) ([]byte, []string, int) { return d, nil, 0 }

func (noopMasker) MaskCell(_ string, v []byte) ([]byte, []string, int) { return v, nil, 0 }

func rule(name string) policy.Rule {
	return policy.Rule{
		Name: name, Type: policy.MatchOperation,
		Operations: []hoopinspect.Operation{hoopinspect.OpDrop},
	}
}

func names(rules []policy.Rule) []string {
	out := make([]string, len(rules))
	for i, r := range rules {
		out[i] = r.Name
	}
	return out
}

// A listener's rules come FIRST so its specific message wins over a generic
// default for the same statement. Every rule type denies and evaluation is
// first-match-wins, so concatenating cannot change the allow/deny outcome,
// only which rule gets reported.
func TestResolveConcatenatesRulesListenerFirst(t *testing.T) {
	cfg := &Config{
		Policy: PolicyConfig{Rules: []policy.Rule{rule("global-a"), rule("global-b")}},
		Listeners: []ListenerConfig{{
			Name: "lane", Protocol: "postgres", Listen: ":1", Upstream: "h:1",
			Policy: &PolicyConfig{Rules: []policy.Rule{rule("lane-a")}},
		}},
	}

	pc, _ := cfg.resolve(cfg.Listeners[0])
	got := names(pc.Rules)
	want := []string{"lane-a", "global-a", "global-b"}

	if len(got) != len(want) {
		t.Fatalf("rules = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rules = %v, want %v", got, want)
		}
	}
}

// The merge must not write through a shared backing array. One listener's
// rules leaking into another's changes policy on a lane nobody edited.
func TestResolveDoesNotAliasAcrossListeners(t *testing.T) {
	cfg := &Config{
		// Capacity beyond length makes append reuse the array.
		Policy: PolicyConfig{Rules: append(make([]policy.Rule, 0, 8), rule("global"))},
		Listeners: []ListenerConfig{
			{Name: "a", Protocol: "postgres", Listen: ":1", Upstream: "h:1",
				Policy: &PolicyConfig{Rules: []policy.Rule{rule("only-a")}}},
			{Name: "b", Protocol: "postgres", Listen: ":2", Upstream: "h:2",
				Policy: &PolicyConfig{Rules: []policy.Rule{rule("only-b")}}},
		},
	}

	pcA, _ := cfg.resolve(cfg.Listeners[0])
	pcB, _ := cfg.resolve(cfg.Listeners[1])

	for _, n := range names(pcA.Rules) {
		if n == "only-b" {
			t.Fatalf("lane a saw lane b's rule: %v", names(pcA.Rules))
		}
	}
	for _, n := range names(pcB.Rules) {
		if n == "only-a" {
			t.Fatalf("lane b saw lane a's rule: %v", names(pcB.Rules))
		}
	}
}

// OPA replaces rather than merges. One lane cannot have two decision
// endpoints, and keeping the default would send statements to a policy the
// operator thought they had overridden.
func TestResolveReplacesOPA(t *testing.T) {
	cfg := &Config{
		Policy: PolicyConfig{OPA: &OPAConfig{URL: "http://default/v1/data/x"}},
		Listeners: []ListenerConfig{
			{Name: "override", Protocol: "http", Listen: ":1", Upstream: "h:1",
				Policy: &PolicyConfig{OPA: &OPAConfig{URL: "http://lane/v1/data/y"}}},
			{Name: "inherit", Protocol: "http", Listen: ":2", Upstream: "h:2"},
		},
	}

	pc, _ := cfg.resolve(cfg.Listeners[0])
	if pc.OPA.URL != "http://lane/v1/data/y" {
		t.Errorf("override lane opa = %q, want the listener's", pc.OPA.URL)
	}
	pc, _ = cfg.resolve(cfg.Listeners[1])
	if pc.OPA.URL != "http://default/v1/data/x" {
		t.Errorf("inherit lane opa = %q, want the default", pc.OPA.URL)
	}
}

// A lane rolling out behind an enforcing default must be able to say
// observe-only. A plain bool cannot express that, so Enforce is a pointer.
func TestResolveListenerCanDisableEnforcementAgainstEnabledDefault(t *testing.T) {
	cfg := &Config{
		Policy: PolicyConfig{Enforce: ptr(true), Rules: []policy.Rule{rule("global")}},
		Listeners: []ListenerConfig{
			{Name: "observing", Protocol: "postgres", Listen: ":1", Upstream: "h:1",
				Policy: &PolicyConfig{Enforce: ptr(false)}},
			{Name: "enforcing", Protocol: "postgres", Listen: ":2", Upstream: "h:2"},
		},
	}

	lanes, err := buildLanes(cfg, nil, nil)
	if err != nil {
		t.Fatalf("buildLanes: %v", err)
	}
	if lanes[0].policy != nil {
		t.Error("listener said enforce:false but got an evaluator")
	}
	if lanes[1].policy == nil {
		t.Error("listener inheriting enforce:true got no evaluator")
	}
}

// Mask rules REPLACE rather than concatenate: a rule owns an entity type, and
// two rules claiming one entity leave slice order to decide the winner.
func TestResolveReplacesMaskRules(t *testing.T) {
	cfg := &Config{
		Mask: MaskConfig{Enabled: ptr(true), Rules: []byte(`[{"entity":"EMAIL_ADDRESS"}]`)},
		Listeners: []ListenerConfig{{
			Name: "lane", Protocol: "http", Listen: ":1", Upstream: "h:1",
			Mask: &MaskConfig{Rules: []byte(`[{"entity":"US_SSN"}]`)},
		}},
	}

	_, mc := cfg.resolve(cfg.Listeners[0])
	if got := string(mc.Rules); got != `[{"entity":"US_SSN"}]` {
		t.Errorf("mask rules = %s, want the listener's list alone", got)
	}
	if !mc.on() {
		t.Error("enabled should be inherited when the listener does not set it")
	}
}

// Each lane gets its own evaluator. A shared one would apply every lane's
// rules to every lane, the bug this change fixes.
func TestBuildLanesGivesEachListenerItsOwnEvaluator(t *testing.T) {
	cfg := &Config{
		Policy: PolicyConfig{Enforce: ptr(true)},
		Listeners: []ListenerConfig{
			{Name: "a", Protocol: "postgres", Listen: ":1", Upstream: "h:1",
				Policy: &PolicyConfig{Rules: []policy.Rule{rule("deny-on-a")}}},
			{Name: "b", Protocol: "postgres", Listen: ":2", Upstream: "h:2"},
		},
	}

	lanes, err := buildLanes(cfg, nil, nil)
	if err != nil {
		t.Fatalf("buildLanes: %v", err)
	}

	drop := hoopinspect.Statement{
		Protocol: hoopinspect.Postgres, Text: "DROP TABLE t", Operation: hoopinspect.OpDrop,
	}
	if v := lanes[0].policy.Evaluate(drop); !v.Denied {
		t.Error("lane a did not enforce its own rule")
	}
	if lanes[1].policy != nil {
		t.Error("lane b inherited a rule set it never configured")
	}
}

// Postgres masking used to be refused because the gate could not re-frame a
// length-prefixed response. The codec does now, so the config must accept it.
// A stale refusal here would beat the original bug for damage: masking that
// works, switched off by validation.
func TestMaskOnPostgresIsAccepted(t *testing.T) {
	cfg := &Config{
		Listeners: []ListenerConfig{{
			Name: "appdb", Protocol: "postgres", Listen: ":1", Upstream: "h:1",
			Mask: &MaskConfig{Enabled: ptr(true), Rules: []byte(`[{"entity":"US_SSN"}]`)},
		}},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("mask.enabled on a postgres listener was refused: %v", err)
	}

	det := stubPlugin{entities: []string{"US_SSN"}}
	if _, berr := buildMasker(*cfg.Listeners[0].Mask, det, hoopinspect.Postgres); berr != nil {
		t.Errorf("buildMasker refused postgres: %v", berr)
	}
}

// A protocol with neither mechanism must still be refused. This guards
// against config that looks active and can never fire.
func TestMaskOnUnmaskableProtocolIsRefused(t *testing.T) {
	det := stubPlugin{entities: []string{"US_SSN"}}
	mc := MaskConfig{Enabled: ptr(true), Rules: []byte(`[{"entity":"US_SSN"}]`)}

	if _, err := buildMasker(mc, det, hoopinspect.Protocol("mysql")); err == nil {
		t.Error("buildMasker accepted a protocol with no codec and no masking path")
	}
}

// The same mask config is fine on HTTP, which can re-tag Content-Length.
func TestMaskOnHTTPIsAccepted(t *testing.T) {
	cfg := &Config{
		Listeners: []ListenerConfig{{
			Name: "api", Protocol: "http", Listen: ":1", Upstream: "h:1",
			Mask: &MaskConfig{Enabled: ptr(true), Rules: []byte(`[{"entity":"US_SSN"}]`)},
		}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("mask on http was rejected: %v", err)
	}

	lanes, err := buildLanes(cfg, stubPlugin{entities: []string{"US_SSN"}}, nil)
	if err != nil {
		t.Fatalf("buildLanes: %v", err)
	}
	if lanes[0].masker == nil {
		t.Error("http lane got no masker")
	}
}

// A pii rule naming an entity the detector was not told to find would
// evaluate cleanly and never match, a guardrail that silently allows what it
// was written to deny.
func TestPIIRuleNamingUndetectedEntityIsRefused(t *testing.T) {
	cfg := &Config{
		Policy: PolicyConfig{Enforce: ptr(true)},
		Listeners: []ListenerConfig{{
			Name: "appdb", Protocol: "postgres", Listen: ":1", Upstream: "h:1",
			Policy: &PolicyConfig{Rules: []policy.Rule{{
				Name: "no-cpf", Type: policy.MatchPII, Entities: []string{"BR_CPF"},
			}}},
		}},
	}

	// The detector only knows US_SSN.
	_, err := buildLanes(cfg, stubPlugin{entities: []string{"US_SSN"}}, nil)
	if err == nil {
		t.Fatal("a pii rule for an undetectable entity was accepted")
	}
	if !strings.Contains(err.Error(), "BR_CPF") {
		t.Errorf("error does not name the entity: %v", err)
	}

	// Same rule, detector configured for it: fine.
	if _, err := buildLanes(cfg, stubPlugin{entities: []string{"BR_CPF"}}, nil); err != nil {
		t.Errorf("a pii rule the detector can serve was rejected: %v", err)
	}
}

// One run reports every broken lane, so you do not fix a fleet config one
// error per restart.
func TestBuildLanesReportsEveryBrokenLane(t *testing.T) {
	cfg := &Config{
		Policy: PolicyConfig{Enforce: ptr(true)},
		Listeners: []ListenerConfig{
			{Name: "bad-regex", Protocol: "postgres", Listen: ":1", Upstream: "h:1",
				Policy: &PolicyConfig{Rules: []policy.Rule{{
					Name: "r", Type: policy.MatchPattern, Pattern: "([unclosed",
				}}}},
			// An entity the plugin does not detect: still a config error now
			// that postgres masking itself is valid.
			{Name: "bad-mask", Protocol: "postgres", Listen: ":2", Upstream: "h:2",
				Mask: &MaskConfig{Enabled: ptr(true), Rules: []byte(`[{"entity":"NOT_A_THING"}]`)}},
		},
	}

	_, err := buildLanes(cfg, stubPlugin{entities: []string{"US_SSN"}}, nil)
	if err == nil {
		t.Fatal("two broken lanes were accepted")
	}
	for _, want := range []string{"bad-regex", "bad-mask"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error omits lane %q: %v", want, err)
		}
	}
}

// A config with no per-listener blocks behaves as it did before this feature
// existed: the top-level policy applies everywhere.
func TestGlobalOnlyConfigStillAppliesToEveryLane(t *testing.T) {
	cfg := &Config{
		Policy: PolicyConfig{Enforce: ptr(true), Rules: []policy.Rule{rule("no-drop")}},
		Listeners: []ListenerConfig{
			{Name: "a", Protocol: "postgres", Listen: ":1", Upstream: "h:1"},
			{Name: "b", Protocol: "http", Listen: ":2", Upstream: "h:2"},
		},
	}

	lanes, err := buildLanes(cfg, nil, nil)
	if err != nil {
		t.Fatalf("buildLanes: %v", err)
	}
	for _, ln := range lanes {
		if ln.policy == nil {
			t.Errorf("lane %s got no evaluator from the global policy", ln.name)
		}
		if len(ln.rules) != 1 || ln.rules[0] != "no-drop" {
			t.Errorf("lane %s rules = %v, want [no-drop]", ln.name, ln.rules)
		}
	}
}
