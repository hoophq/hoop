package services

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/policy"
)

// effectiveLane is what one lane enforces, resolved the way the daemon's
// config.go resolve does: guardrails are the lane's rules then the top-level
// ones unless the lane says `rules: []`; a lane mask list replaces the
// top-level one.
type effectiveLane struct {
	Guardrails []string
	Mask       string
	Analyzer   *daemon.LaneAnalyzerConfig
}

func resolveForTest(t *testing.T, cfg daemon.Config) map[string]effectiveLane {
	t.Helper()
	out := map[string]effectiveLane{}
	for _, l := range cfg.Listeners {
		var rules []policy.Rule
		if cfg.Guardrails != nil {
			rules = cfg.Guardrails.Rules
		}
		if o := l.Guardrails; o != nil && o.Rules != nil {
			if len(o.Rules) == 0 {
				rules = nil
			} else {
				rules = append(append([]policy.Rule{}, o.Rules...), rules...)
			}
		}
		var names []string
		for _, r := range rules {
			names = append(names, r.Name)
		}
		var mask json.RawMessage
		if cfg.Mask != nil {
			mask = cfg.Mask.Rules
		}
		if l.Mask != nil && len(l.Mask.Rules) > 0 {
			mask = l.Mask.Rules
		}
		var entries []json.RawMessage
		if len(mask) > 0 {
			if err := json.Unmarshal(mask, &entries); err != nil {
				t.Fatalf("mask on %q: %v", l.Name, err)
			}
		}
		canon, _ := json.Marshal(entries)
		out[l.Name] = effectiveLane{Guardrails: names, Mask: string(canon), Analyzer: l.Analyzer}
	}
	return out
}

func importFixture() daemon.Config {
	gr := func(name string) policy.Rule {
		return policy.Rule{Name: name, Type: "deny_words_list", Words: []string{"x"}}
	}
	return daemon.Config{
		Guardrails: &daemon.GuardrailsConfig{Mode: "observe", Rules: []policy.Rule{gr("top-a"), gr("top-b")}},
		Mask:       &daemon.MaskConfig{Rules: json.RawMessage(`[{"name":"emails","entities":["EMAIL_ADDRESS"],"strategy":"redact"}]`)},
		Listeners: []daemon.ListenerConfig{
			{Name: "appdb", Protocol: "postgres",
				Guardrails: &daemon.GuardrailsConfig{Rules: []policy.Rule{gr("lane-a")}},
				Analyzer: &daemon.LaneAnalyzerConfig{
					HighRisk: "require_review", ApprovalRule: "dba", MaxCalls: 40,
					Trigger: &policy.AITrigger{Tables: []string{"payments"}}, Prompt: "p",
				}},
			{Name: "optout", Protocol: "mysql",
				Guardrails: &daemon.GuardrailsConfig{Rules: []policy.Rule{}},
				Mask:       &daemon.MaskConfig{Rules: json.RawMessage(`[]`)}},
			{Name: "own-mask", Protocol: "postgres",
				Mask: &daemon.MaskConfig{Rules: json.RawMessage(`[{"columns":["ssn"],"strategy":"hash"}]`)}},
		},
	}
}

func TestSplitSidecarConfigurationServesTheSameDocument(t *testing.T) {
	cfg := importFixture()
	want := resolveForTest(t, cfg)

	stripped, items, err := SplitSidecarConfiguration("edge", cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	var guardrails, masking, analyzers []models.BoundRule
	for _, it := range items {
		for _, l := range it.Listeners {
			b := models.BoundRule{RuleName: it.Name, ListenerName: l, Spec: it.Spec}
			switch it.Kind {
			case SidecarRuleGuardrail:
				guardrails = append(guardrails, b)
			case SidecarRuleMask:
				masking = append(masking, b)
			case SidecarRuleAnalyzer:
				analyzers = append(analyzers, b)
			}
		}
	}
	// Nothing enforces from the stripped document alone.
	for lane, got := range resolveForTest(t, stripped) {
		if len(got.Guardrails) != 0 || (got.Mask != "null" && got.Mask != "[]") {
			t.Errorf("lane %q still carries file rules after the split: %+v", lane, got)
		}
	}

	folded, err := foldSidecarRules(stripped, guardrails, masking, analyzers)
	if err != nil {
		t.Fatal(err)
	}
	got := resolveForTest(t, folded)
	for lane, w := range want {
		g := got[lane]
		if !reflect.DeepEqual(g.Guardrails, w.Guardrails) {
			t.Errorf("lane %q guardrails: got %v, want %v", lane, g.Guardrails, w.Guardrails)
		}
		if g.Mask != w.Mask {
			t.Errorf("lane %q mask: got %s, want %s", lane, g.Mask, w.Mask)
		}
	}

	// The analyzer keeps its decision, and the hold points at the new rule.
	a := got["appdb"].Analyzer
	if a == nil || a.HighRisk != "require_review" || a.MaxCalls != 40 || a.Prompt != "p" ||
		a.Trigger == nil || a.ApprovalRule != "edge-appdb-analyzer" {
		t.Errorf("appdb analyzer after the fold: %+v", a)
	}
	if stripped.Guardrails == nil || stripped.Guardrails.Mode != "observe" {
		t.Errorf("the top-level guardrails mode must stay in the document: %+v", stripped.Guardrails)
	}
}

func TestSplitSidecarConfigurationItems(t *testing.T) {
	_, items, err := SplitSidecarConfiguration("Edge 1", importFixture(), func(kind SidecarRuleKind, name string) bool {
		return kind == SidecarRuleGuardrail && name == "edge-1-top-a"
	})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, it := range items {
		got[string(it.Kind)+" "+it.Name] = it.Listeners
	}
	want := map[string][]string{
		"guardrail edge-1-appdb-lane-a":       {"appdb"},
		"guardrail edge-1-top-a-2":            {"appdb", "own-mask"},
		"guardrail edge-1-top-b":              {"appdb", "own-mask"},
		"data masking edge-1-emails":          {"appdb"},
		"data masking edge-1-own-mask-mask-1": {"own-mask"},
		"analyzer edge-1-appdb-analyzer":      {"appdb"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("items:\n got %v\nwant %v", got, want)
	}
}

func TestSlugRuleName(t *testing.T) {
	for in, want := range map[string]string{
		"Edge 1-appdb-No DROP!": "edge-1-appdb-no-drop",
		"a":                     "a__",
		"x..y--z":               "x-y-z",
	} {
		if got := slugRuleName(in); got != want {
			t.Errorf("slugRuleName(%q) = %q, want %q", in, got, want)
		}
	}
}
