package daemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

// The served document decodes strictly, so the keys must be declared fields.
func TestTriggerItemsDecodeStrictly(t *testing.T) {
	var tr policy.AITrigger
	dec := json.NewDecoder(strings.NewReader(`{
		"operations": ["delete"],
		"any": [{"operations": ["patch"], "resources": ["/api/**"]}],
		"exclude": [{"resources": ["/api/healthz"]}]
	}`))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&tr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(tr.Any) != 1 || len(tr.Any[0].Resources) != 1 || len(tr.Exclude) != 1 {
		t.Fatalf("decoded %+v", tr)
	}
}

// End to end through buildLaneAnalyzer: an exclude-only trigger on an
// ungated lane classifies everything except what it names.
func TestExcludeOnlyClassifiesEverythingElse(t *testing.T) {
	rec := &recordingProvider{}
	deps := &analyzerDeps{cfg: &AnalyzerConfig{Provider: "stub", Model: "m"}, provider: rec}
	la := &LaneAnalyzerConfig{HighRisk: "block", Trigger: &policy.AITrigger{
		Exclude: []policy.AITriggerItem{{Tables: []string{"audit_log"}}},
	}}
	ev, err := buildLaneAnalyzer("appdb", la, deps, false, false, nil)
	if err != nil {
		t.Fatalf("buildLaneAnalyzer: %v", err)
	}
	sel := func(table string) inspect.Statement {
		return inspect.Statement{
			Protocol: inspect.Postgres, Direction: inspect.FromClient,
			Text: "SELECT * FROM " + table, Operation: inspect.OpSelect, Tables: []string{table},
		}
	}
	ev.Evaluate(sel("audit_log"))
	ev.Evaluate(sel("users"))

	if got := rec.sent(); len(got) != 1 || !strings.Contains(got[0], "users") {
		t.Fatalf("classified %q, want only the users statement", got)
	}
}

func TestAnEmptyTriggerItemIsRefusedAtLoad(t *testing.T) {
	empty := []policy.AITriggerItem{{}}
	for name, tr := range map[string]*policy.AITrigger{
		"any":     {Any: empty},
		"exclude": {Operations: []inspect.Operation{inspect.OpDelete}, Exclude: empty},
	} {
		la := laneBlock()
		la.Trigger = tr
		err := blockLane(la).Validate()
		if err == nil || !strings.Contains(err.Error(), "trigger."+name+"[0] names no") {
			t.Errorf("%s: Validate = %v, want an empty-item refusal", name, err)
		}
		// The control plane validates what it stores through the export.
		if got := ValidateLaneAnalyzerBlock(la, "appdb"); len(got) == 0 {
			t.Errorf("%s: ValidateLaneAnalyzerBlock accepted an empty item", name)
		}
	}

	rule := aiRule("risky")
	rule.Trigger = &policy.AITrigger{Any: empty}
	cfg := pgLane(rule)
	cfg.Analyzer = &AnalyzerConfig{Provider: "stub", Model: "m"}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "trigger.any[0]") {
		t.Errorf("rule form: Validate = %v, want an empty-item refusal", err)
	}
}

// On a gated lane the gate replaces the trigger and silence selects nothing,
// so an exclude with nothing else to narrow is never read.
func TestExcludeAloneOnAGatedLaneIsRefused(t *testing.T) {
	gated := func(tr *policy.AITrigger) error {
		la := laneBlock()
		la.Trigger = tr
		cfg := blockLane(la)
		cfg.Listeners[0].OPA = &OPAConfig{URL: "http://opa:8181/v1/data/hoop", Gate: true}
		return cfg.Validate()
	}
	exclude := []policy.AITriggerItem{{Tables: []string{"audit_log"}}}

	err := gated(&policy.AITrigger{Exclude: exclude})
	if err == nil || !strings.Contains(err.Error(), "trigger.exclude narrows nothing") {
		t.Fatalf("Validate = %v, want the gated exclude refusal", err)
	}
	if err := gated(&policy.AITrigger{
		Operations: []inspect.Operation{inspect.OpDelete}, Exclude: exclude,
	}); err != nil {
		t.Fatalf("exclude beside an operation on a gated lane was refused: %v", err)
	}
}

// An item naming an operation the lane never classifies is a trigger that
// never fires, so the scoped-protocol check (ssh) must see item operations.
// Exclude operations stay out: excluding what is never classified is
// harmless.
func TestItemOperationsReachTheClassifiableCheck(t *testing.T) {
	lc := ListenerConfig{Analyzer: &LaneAnalyzerConfig{Trigger: &policy.AITrigger{
		Operations: []inspect.Operation{inspect.OpDelete},
		Any:        []policy.AITriggerItem{{Operations: []inspect.Operation{inspect.OpPatch}}},
		Exclude:    []policy.AITriggerItem{{Operations: []inspect.Operation{inspect.OpGet}}},
	}}}
	rule := aiRule("risky")
	rule.Trigger = &policy.AITrigger{Any: []policy.AITriggerItem{
		{Operations: []inspect.Operation{inspect.OpPost}},
	}}

	got := joinOperations(analyzerTriggerOperations(lc, []policy.Rule{rule}))
	if got != "delete, patch, post" {
		t.Fatalf("operations = %q, want delete, patch, post", got)
	}
}

func TestTriggerItemsAreServedOnlyToABuildThatDecodesThem(t *testing.T) {
	item := []policy.AITriggerItem{{Tables: []string{"audit_log"}}}
	anyLane, excludeLane := laneBlock(), laneBlock()
	anyLane.Trigger.Any = item
	excludeLane.Trigger.Exclude = item

	for name, la := range map[string]*LaneAnalyzerConfig{"any": anyLane, "exclude": excludeLane} {
		cfg := blockLane(la)
		if err := CheckServable(*cfg, Handshake{Capabilities: SidecarCapabilities()}); err != nil {
			t.Errorf("%s: a current build was refused: %v", name, err)
		}
		err := CheckServable(*cfg, Handshake{Capabilities: []string{CapabilityReviewMode}})
		if err == nil || !strings.Contains(err.Error(), CapabilityAnalyzerTriggerItems) {
			t.Errorf("%s: CheckServable = %v, want a refusal naming %s",
				name, err, CapabilityAnalyzerTriggerItems)
		}
	}
}

// The control plane's view: the lane inherits opa.gate from the top level, and
// the export resolves it the way the sidecar does at load.
func TestValidateLaneTriggersReadsAnInheritedGate(t *testing.T) {
	la := laneBlock()
	la.Trigger = &policy.AITrigger{Exclude: []policy.AITriggerItem{{Tables: []string{"audit_log"}}}}
	cfg := blockLane(la)
	if got := ValidateLaneTriggers(*cfg); len(got) != 0 {
		t.Fatalf("an ungated lane was refused: %v", got)
	}
	cfg.OPA = &OPAConfig{URL: "http://opa:8181/v1/data/hoop", Gate: true}
	if got := ValidateLaneTriggers(*cfg); len(got) != 1 {
		t.Fatalf("ValidateLaneTriggers = %v, want the gated exclude refusal", got)
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("the sidecar accepted what the plane refuses")
	}
}
