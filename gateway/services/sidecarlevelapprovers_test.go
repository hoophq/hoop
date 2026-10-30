package services

import (
	"errors"
	"strings"
	"testing"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/daemon"
)

func approversLane(approvalRule string) daemon.Config {
	return daemon.Config{Listeners: []daemon.ListenerConfig{{
		Name: "appdb", Protocol: "postgres",
		Analyzer: &daemon.LaneAnalyzerConfig{HighRisk: "require_review", ApprovalRule: approvalRule,
			Approvers: &daemon.LevelApprovers{High: &daemon.LevelApprover{Type: analyzer.ApproverAIAgent}}},
	}}}
}

// The save checks what the handshake serves: a build that does not read
// approvers gets the held levels under approval_rule, or, with none named,
// a refusal that names the capability.
func TestCheckSidecarCapabilitiesServesLevelApproversAsTheHandshakeDoes(t *testing.T) {
	old := &models.Sidecar{Name: "edge", Capabilities: []string{}}

	withRule := approversLane("dba-rule")
	if err := CheckSidecarCapabilities(old, withRule); err != nil {
		t.Errorf("an old build with an approval_rule is served the bare holds, got %v", err)
	}
	if withRule.Listeners[0].Analyzer.Approvers == nil {
		t.Error("the check changed the composed document")
	}

	err := CheckSidecarCapabilities(old, approversLane(""))
	var missing ErrSidecarCapabilityMissing
	if !errors.As(err, &missing) || !strings.Contains(err.Error(), daemon.CapabilityLevelApprovers) {
		t.Errorf("an old build with approvers and no approval_rule must be refused naming %s, got %v",
			daemon.CapabilityLevelApprovers, err)
	}

	current := &models.Sidecar{Name: "edge", Capabilities: []string{daemon.CapabilityLevelApprovers}}
	if err := CheckSidecarCapabilities(current, approversLane("")); err != nil {
		t.Errorf("a build that reads approvers is served them, got %v", err)
	}
}

// approvers belong to the levels, so the rule's replace the listener's.
func TestAnAnalyzerRuleCarriesItsLevelApprovers(t *testing.T) {
	base := &daemon.LaneAnalyzerConfig{MaxCalls: 10,
		Approvers: &daemon.LevelApprovers{Low: &daemon.LevelApprover{Type: analyzer.ApproverHuman}}}
	rule := daemon.LaneAnalyzerConfig{HighRisk: "require_review",
		Approvers: &daemon.LevelApprovers{High: &daemon.LevelApprover{Type: analyzer.ApproverHuman, Groups: []string{"dba"}}}}

	got := mergeAnalyzerBlock(base, rule)
	if got.LevelApprover(analyzer.RiskHigh) == nil || got.LevelApprover(analyzer.RiskLow) != nil {
		t.Errorf("merged approvers = %+v, want the rule's only", got.Approvers)
	}
	if got.MaxCalls != 10 {
		t.Errorf("the listener's max_calls was lost: %d", got.MaxCalls)
	}

	got = mergeAnalyzerBlock(base, daemon.LaneAnalyzerConfig{HighRisk: "block"})
	if got.Approvers != nil {
		t.Errorf("a rule with no approvers keeps the listener's: %+v", got.Approvers)
	}
}

// An import moves approvers into the analyzer rule with the levels.
func TestSplitAnalyzerMovesTheLevelApprovers(t *testing.T) {
	cfg := approversLane("")
	stripped, items, err := SplitSidecarConfiguration("edge", cfg, nil)
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
	if rule.LevelApprover(analyzer.RiskHigh) == nil {
		t.Errorf("the rule lost the approvers: %+v", rule)
	}
	for _, l := range stripped.Listeners {
		if l.Analyzer != nil && l.Analyzer.Approvers != nil {
			t.Errorf("the lane kept the approvers: %+v", l.Analyzer.Approvers)
		}
	}
}
