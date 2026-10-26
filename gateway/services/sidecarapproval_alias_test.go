package services

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/daemon"
)

// A rule saved with require_approval holds, so the plane creates its approval
// rule. Compared as written, it read as "holds nothing" and every held
// statement was denied.
func TestAnAnalyzerRuleWrittenWithRequireApprovalHolds(t *testing.T) {
	if !AnalyzerRuleHolds(json.RawMessage(`{"high":"require_approval","approval_rule":"r"}`)) {
		t.Error("require_approval did not read as a hold")
	}
	if !AnalyzerRuleHolds(json.RawMessage(`{"high":"require_review","approval_rule":"r"}`)) {
		t.Error("require_review did not read as a hold")
	}
	if AnalyzerRuleHolds(json.RawMessage(`{"high":"block"}`)) {
		t.Error("block read as a hold")
	}
}

// Every reader decodes a spec through decodeSpec, so it sees only the
// canonical spellings.
func TestDecodeSpecFoldsTheApprovalSpellings(t *testing.T) {
	var block daemon.LaneAnalyzerConfig
	err := decodeSpec(json.RawMessage(`{"high":"require_approval","approval_rule":"r","approval_mode":"return"}`), &block)
	if err != nil {
		t.Fatal(err)
	}
	if block.HighRisk.Action != "require_review" || block.ReviewMode != "return" || block.ApprovalMode != "" {
		t.Errorf("decoded %+v, want require_review and review_mode return", block)
	}

	err = decodeSpec(json.RawMessage(`{"high":"require_review","approval_rule":"r","approval_mode":"return","review_mode":"hold"}`), &block)
	if err == nil || !strings.Contains(err.Error(), "not both") {
		t.Errorf("a conflict decoded: %v", err)
	}
}

// A rule's approval_mode reaches the served lane. The merge copies only
// review_mode, so before the fold it was dropped and served as hold.
func TestARuleApprovalModeIsServed(t *testing.T) {
	cfg := daemon.Config{Listeners: []daemon.ListenerConfig{{
		Name: "agents", Protocol: "postgres",
		Analyzer: &daemon.LaneAnalyzerConfig{MaxCalls: 5},
	}}}
	analyzers := []models.BoundRule{{RuleName: "hold", ListenerName: "agents",
		Spec: json.RawMessage(`{"high":"require_approval","approval_rule":"r","approval_mode":"return"}`)}}
	composed, err := foldSidecarRules(cfg, nil, nil, analyzers)
	if err != nil {
		t.Fatal(err)
	}
	la := composed.Listeners[0].Analyzer
	if la.ReviewMode != "return" || la.HighRisk.Action != "require_review" || la.ApprovalMode != "" {
		t.Errorf("served block %+v, want review_mode return and require_review", la)
	}
}

// The plane validates a spec without normalize; an approval_mode no sidecar
// accepts, or one that disagrees with review_mode, is refused at the save.
func TestASpecWithABadApprovalModeIsRefused(t *testing.T) {
	for spec, want := range map[string]string{
		`{"high":"require_review","approval_rule":"r","approval_mode":"bogus"}`:                       "bogus",
		`{"high":"require_review","approval_rule":"r","approval_mode":"return","review_mode":"hold"}`: "not both",
	} {
		err := ValidateSidecarRuleSpec(SidecarRuleAnalyzer, "r", json.RawMessage(spec))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want an error naming %q", spec, err, want)
		}
	}
	if err := ValidateSidecarRuleSpec(SidecarRuleAnalyzer, "r",
		json.RawMessage(`{"high":"require_approval","approval_rule":"r","approval_mode":"return"}`)); err != nil {
		t.Errorf("a valid approval spec was refused: %v", err)
	}
}

// The stored document holds only canonical spellings, so a lane approval_mode
// and a rule review_mode cannot both reach the served block.
func TestParseSidecarConfigurationStoresTheCanonicalSpellings(t *testing.T) {
	cfg, err := ParseSidecarConfiguration(json.RawMessage(`{"listeners":[{"name":"agents",` +
		`"analyzer":{"high":"require_approval","approval_rule":"r","approval_mode":"return"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	la := cfg.Listeners[0].Analyzer
	if la.HighRisk.Action != "require_review" || la.ReviewMode != "return" || la.ApprovalMode != "" {
		t.Errorf("stored block %+v, want the canonical spellings", la)
	}

	_, err = ParseSidecarConfiguration(json.RawMessage(`{"listeners":[{"name":"agents",` +
		`"analyzer":{"approval_mode":"return","review_mode":"hold"}}]}`))
	if err == nil || !strings.Contains(err.Error(), "agents: analyzer") {
		t.Errorf("a conflict was stored: %v", err)
	}
}

// An imported lane written with the approval spellings splits like one
// written with the review ones: the rule holds, gets its approval_rule, and
// carries the mode.
func TestSplitAnalyzerReadsTheApprovalSpellings(t *testing.T) {
	cfg := daemon.Config{Listeners: []daemon.ListenerConfig{{
		Name:     "agents",
		Analyzer: &daemon.LaneAnalyzerConfig{HighRisk: daemon.RiskLevel{Action: "require_approval"}, ApprovalMode: "return", MaxCalls: 5},
	}}}
	_, items, err := SplitSidecarConfiguration("edge", cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	var rule daemon.LaneAnalyzerConfig
	if err := json.Unmarshal(items[0].Spec, &rule); err != nil {
		t.Fatal(err)
	}
	if rule.HighRisk.Action != "require_review" || rule.ReviewMode != "return" || rule.ApprovalRule == "" {
		t.Errorf("split rule %+v, want a hold with its mode and an approval_rule", rule)
	}
	if cfg.Listeners[0].Analyzer.ApprovalMode != "return" {
		t.Error("the split wrote back into the caller's document")
	}
}
