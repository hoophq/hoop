package services

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/daemon"
)

func handoffLane(approvalRule string) daemon.Config {
	return daemon.Config{Listeners: []daemon.ListenerConfig{{
		Name: "appdb", Protocol: "postgres",
		Analyzer: &daemon.LaneAnalyzerConfig{HighRisk: "ai-handoff", ApprovalRule: approvalRule,
			Handoffs: &daemon.LevelHandoffs{High: &daemon.LevelHandoff{
				AllowedActions: []string{"allow", "require_approval"}}}},
	}}}
}

// No build before the hand-off reads handoffs, so the save refuses a
// hand-off level to it, with or without an approval_rule.
func TestCheckSidecarCapabilitiesRefusesAHandoffToAnOldBuild(t *testing.T) {
	for _, caps := range [][]string{{}, {daemon.CapabilityLevelApprovers}} {
		old := &models.Sidecar{Name: "edge", Capabilities: caps}
		for _, rule := range []string{"", "dba-rule"} {
			err := CheckSidecarCapabilities(old, handoffLane(rule))
			var missing ErrSidecarCapabilityMissing
			if !errors.As(err, &missing) || !strings.Contains(err.Error(), daemon.CapabilityAIHandoff) {
				t.Errorf("caps %v, approval_rule %q: want a refusal naming %s, got %v",
					caps, rule, daemon.CapabilityAIHandoff, err)
			}
		}
	}
	current := &models.Sidecar{Name: "edge",
		Capabilities: []string{daemon.CapabilityLevelApprovers, daemon.CapabilityAIHandoff}}
	if err := CheckSidecarCapabilities(current, handoffLane("dba-rule")); err != nil {
		t.Errorf("a build that reads hand-offs is served them, got %v", err)
	}
}

// handoffs belong to the levels, so the rule's replace the listener's.
func TestAnAnalyzerRuleCarriesItsHandoffs(t *testing.T) {
	base := &daemon.LaneAnalyzerConfig{MaxCalls: 10,
		Handoffs: &daemon.LevelHandoffs{Low: &daemon.LevelHandoff{AllowedActions: []string{"allow"}}}}
	rule := *handoffLane("").Listeners[0].Analyzer

	got := mergeAnalyzerBlock(base, rule)
	if got.LevelHandoff(analyzer.RiskHigh) == nil || got.LevelHandoff(analyzer.RiskLow) != nil {
		t.Errorf("merged handoffs = %+v, want the rule's only", got.Handoffs)
	}
	if got.MaxCalls != 10 {
		t.Errorf("the listener's max_calls was lost: %d", got.MaxCalls)
	}

	got = mergeAnalyzerBlock(base, daemon.LaneAnalyzerConfig{HighRisk: "block"})
	if got.Handoffs != nil {
		t.Errorf("a rule with no handoffs keeps the listener's: %+v", got.Handoffs)
	}
}

// An import moves handoffs into the analyzer rule with the levels, and names
// the rule's own approval rule for the hold the hand-off may select.
func TestSplitAnalyzerMovesTheHandoffs(t *testing.T) {
	stripped, items, err := SplitSidecarConfiguration("edge", handoffLane(""), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want the analyzer rule", len(items))
	}
	var rule daemon.LaneAnalyzerConfig
	if err := decodeSpec(items[0].Spec, &rule); err != nil {
		t.Fatal(err)
	}
	if rule.LevelHandoff(analyzer.RiskHigh) == nil {
		t.Errorf("the rule lost the handoffs: %+v", rule)
	}
	if rule.ApprovalRule != items[0].Name {
		t.Errorf("approval_rule = %q, want the rule's own %q", rule.ApprovalRule, items[0].Name)
	}
	for _, l := range stripped.Listeners {
		if l.Analyzer != nil && l.Analyzer.Handoffs != nil {
			t.Errorf("the lane kept the handoffs: %+v", l.Analyzer.Handoffs)
		}
	}
}

// A hand-off level that may select require_approval holds, whoever approves:
// the managed rule exists for it, as for a direct hold.
func TestAnAnalyzerRuleHoldsThroughAHandoff(t *testing.T) {
	for spec, want := range map[string]bool{
		`{"high":"require_approval","approval_rule":"r"}`: true,
		`{"high":"warn"}`: false,
		`{"high":"ai-handoff","handoffs":{"high":{"allowed_actions":["allow","require_approval"]}}}`:   true,
		`{"high":"ai-handoff","handoffs":{"high":{"allowed_actions":["require_review"]}}}`:            true,
		`{"high":"ai-handoff","handoffs":{"high":{"allowed_actions":["require_approval"]}},` +
			`"approvers":{"high":{"type":"ai_agent"}}}`: true,
		`{"high":"ai-handoff","handoffs":{"high":{"allowed_actions":["allow","block"]}}}`: false,
		`{"high":"ai-handoff"}`: false,
		`{"high":"block","handoffs":{"high":{"allowed_actions":["require_approval"]}}}`: false,
	} {
		if got := AnalyzerRuleHolds(json.RawMessage(spec)); got != want {
			t.Errorf("AnalyzerRuleHolds(%s) = %v, want %v", spec, got, want)
		}
	}
}
