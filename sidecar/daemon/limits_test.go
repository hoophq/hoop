package daemon

import (
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/policy"
)

// limitsLane is a minimal valid listener, so a limits test fails on the limit
// it is about and not on a missing upstream.
func limitsLane(name, listen string) ListenerConfig {
	return ListenerConfig{
		Name: name, Protocol: "postgres", Listen: listen, Upstream: "h:5432",
	}
}

func maskRule(entity string) []byte {
	return []byte(`[{"name":"r","entity":"` + entity + `","strategy":"redact"}]`)
}

// The cap fires on the second guardrail rule, and the message says where both
// of them are. A bare count sends an operator reading a config from the top,
// which is usually the rule they meant to keep.
func TestSecondGuardrailRuleIsRefused(t *testing.T) {
	lane := limitsLane("appdb", ":1")
	lane.Guardrails = &GuardrailsConfig{Rules: []policy.Rule{rule("no-drop-on-appdb")}}
	cfg := &Config{
		Guardrails: &GuardrailsConfig{Rules: []policy.Rule{rule("no-drop")}},
		Listeners:  []ListenerConfig{lane},
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("two guardrail rules were accepted")
	}
	for _, want := range []string{"2 guardrail rules", "guardrails: 1", "appdb: 1", "at most 1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message does not contain %q: %v", want, err)
		}
	}
}

// The deprecated `policy` block folds onto `guardrails` before anything
// validates, so the cap counts the same rule once however it was spelled. A
// counter reading both fields would refuse a one-rule config written the old
// way.
func TestDeprecatedPolicySpellingCountsOnce(t *testing.T) {
	p := writeConfig(t, `{
      "listeners": [{"protocol":"postgres","listen":":1","upstream":"h:1"}],
      "policy": {"rules": [{"name":"no-drop","type":"operation","operations":["drop"]}]}
    }`)

	if _, err := LoadConfig(p); err != nil {
		t.Fatalf("one rule in the deprecated spelling was refused: %v", err)
	}
}

// One rule is one rule however many lanes inherit it. resolve() copies the
// defaults into every lane, so a cap counting RESOLVED lanes would report
// this config as two rules and refuse a free-tier config that holds one.
func TestOneGlobalRuleAcrossTwoLanesIsAccepted(t *testing.T) {
	cfg := &Config{
		Guardrails: &GuardrailsConfig{Mode: ModeEnforce, Rules: []policy.Rule{rule("no-drop")}},
		Listeners:  []ListenerConfig{limitsLane("a", ":1"), limitsLane("b", ":2")},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("one global rule inherited by two lanes was refused: %v", err)
	}
}

// ai_analysis rules share a slice with guardrails and are not guardrails.
// They leave the process and cost money per statement, and their controls are
// the trigger and the max_calls budget, not a count. Capping the slice would
// cap them by accident.
func TestAIAnalysisRulesAreNotCountedAsGuardrails(t *testing.T) {
	cfg := &Config{
		Guardrails: &GuardrailsConfig{Rules: []policy.Rule{
			rule("no-drop"), aiRule("risky-1"), aiRule("risky-2"),
		}},
		Listeners: []ListenerConfig{limitsLane("appdb", ":1")},
	}

	if problems := cfg.checkLimits(); len(problems) != 0 {
		t.Errorf("ai_analysis rules counted against the guardrail cap: %v", problems)
	}
}

// Masking is capped the same way and across the same scope. mask.rules
// REPLACES rather than concatenates, so a global block and a lane block are
// two authored rules even though no single byte path sees both.
func TestSecondMaskRuleIsRefused(t *testing.T) {
	lane := limitsLane("appdb", ":1")
	lane.Mask = &MaskConfig{Rules: maskRule("US_SSN")}
	cfg := &Config{
		Mask:      &MaskConfig{Rules: maskRule("EMAIL_ADDRESS")},
		Listeners: []ListenerConfig{lane},
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("two data masking rules were accepted")
	}
	for _, want := range []string{"2 data masking rules", "mask: 1", "appdb: 1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message does not contain %q: %v", want, err)
		}
	}
}

// `rules: []` is how a lane switches inherited masking off, so it authors
// nothing and must not spend the one rule this build allows. The count is a
// decode rather than len() on the raw JSON, which those two bytes would pass.
func TestLaneOptingOutOfMaskingCostsNothing(t *testing.T) {
	lane := limitsLane("appdb", ":1")
	lane.Mask = &MaskConfig{Rules: []byte(`[]`)}
	cfg := &Config{
		Mask:      &MaskConfig{Rules: maskRule("EMAIL_ADDRESS")},
		Listeners: []ListenerConfig{lane},
	}

	if problems := cfg.checkLimits(); len(problems) != 0 {
		t.Errorf("an empty override was counted as a rule: %v", problems)
	}
}

// A mask block that is not an array is named once, against the block that
// authored it. Reporting it per inheriting lane would print one typo as many
// times as there are listeners.
func TestMalformedMaskRulesAreReportedOnceAtTheirSite(t *testing.T) {
	cfg := &Config{
		Mask:      &MaskConfig{Rules: []byte(`{"entity":"US_SSN"}`)},
		Listeners: []ListenerConfig{limitsLane("a", ":1"), limitsLane("b", ":2")},
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("a mask block that is not an array was accepted")
	}
	if n := strings.Count(err.Error(), "mask.rules: not a JSON array"); n != 1 {
		t.Errorf("reported %d times, want 1: %v", n, err)
	}
}

// Run and the exported Validate never call (*Config).Validate, so a cap that
// lived only in the file validator would be skipped by every caller that
// assembles a Config in Go. buildLanes is the one function all of them reach.
func TestLimitsHoldWhenTheFileValidatorIsSkipped(t *testing.T) {
	lane := limitsLane("appdb", ":1")
	lane.Guardrails = &GuardrailsConfig{Rules: []policy.Rule{rule("second")}}
	cfg := &Config{
		Guardrails: &GuardrailsConfig{Mode: ModeEnforce, Rules: []policy.Rule{rule("first")}},
		Listeners:  []ListenerConfig{lane},
	}

	if _, err := buildLanes(cfg, nil, nil); err == nil {
		t.Fatal("buildLanes accepted a config over the guardrail cap")
	} else if !strings.Contains(err.Error(), "guardrail rules") {
		t.Errorf("error does not name the cap: %v", err)
	}
}

// Every problem in one run: a config over both caps reports both, and still
// reports the unrelated lane errors beside them.
func TestLimitsAreReportedWithEveryOtherProblem(t *testing.T) {
	lane := limitsLane("appdb", ":1")
	lane.Guardrails = &GuardrailsConfig{Rules: []policy.Rule{rule("second")}}
	lane.Mask = &MaskConfig{Rules: maskRule("US_SSN")}
	cfg := &Config{
		Guardrails: &GuardrailsConfig{Rules: []policy.Rule{rule("first")}},
		Mask:       &MaskConfig{Rules: maskRule("EMAIL_ADDRESS")},
		Listeners:  []ListenerConfig{lane, {Name: "broken"}},
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("an invalid config was accepted")
	}
	for _, want := range []string{"guardrail rules", "data masking rules", "broken: no protocol"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message does not contain %q: %v", want, err)
		}
	}
}

// The caps are reported, not just enforced. An operator who has to discover a
// limit by hitting it learns it in the worst place.
func TestLimitsSummaryNamesBothCaps(t *testing.T) {
	got := LimitsSummary()
	for _, want := range []string{"1 guardrail rule(s)", "1 data masking rule(s)"} {
		if !strings.Contains(got, want) {
			t.Errorf("LimitsSummary() = %q, missing %q", got, want)
		}
	}
}

func TestCountMaskRules(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    int
		wantErr bool
	}{
		{"absent", "", 0, false},
		{"null", "null", 0, false},
		{"empty array", "[]", 0, false},
		{"one rule", `[{"name":"a"}]`, 1, false},
		{"two rules", `[{"name":"a"},{"name":"b"}]`, 2, false},
		{"object not array", `{"name":"a"}`, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := countMaskRules([]byte(tc.raw))
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("count = %d, want %d", got, tc.want)
			}
		})
	}
}
