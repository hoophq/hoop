package daemon

import (
	"slices"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
)

func TestMinApprovalsDecodesAndValidates(t *testing.T) {
	doc := `{"listeners":[{"name":"appdb","protocol":"postgres","listen":":1","upstream":"h:1",
		"analyzer":{"high":"require_approval","medium":"require_approval",
			"approvers":{
				"high":{"type":"human","owners":true,"groups":["leads"],"all_groups_must_approve":true},
				"medium":{"type":"human","groups":["dba","sre","risk"],"min_approvals":2}}}}]}`
	cfg, err := LoadConfigBytes([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	cfg.ControlPlaneURL = "https://cp.example.com"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !cfg.Listeners[0].Analyzer.LevelApprover(analyzer.RiskHigh).AllGroupsMustApprove {
		t.Fatal("high lost all_groups_must_approve")
	}
	if cfg.Listeners[0].Analyzer.LevelApprover(analyzer.RiskMedium).MinApprovals != 2 {
		t.Fatal("medium lost min_approvals")
	}

	for name, tc := range map[string]struct {
		la   *LaneAnalyzerConfig
		want string
	}{
		"on agent": {approverBlock(LevelApprovers{High: &LevelApprover{Type: analyzer.ApproverAIAgent, AllGroupsMustApprove: true}}),
			"approval count does not apply"},
		"both set": {approverBlock(LevelApprovers{High: &LevelApprover{Type: analyzer.ApproverHuman, Groups: []string{"a", "b"},
			AllGroupsMustApprove: true, MinApprovals: 2}}), "all_groups_must_approve and min_approvals are alternatives"},
		"negative": {approverBlock(LevelApprovers{High: &LevelApprover{Type: analyzer.ApproverHuman, Groups: []string{"a"},
			MinApprovals: -1}}), "min_approvals must be at least 1"},
		"exceeds groups": {approverBlock(LevelApprovers{High: &LevelApprover{Type: analyzer.ApproverHuman, Owners: true, Groups: []string{"leads"},
			MinApprovals: 3}}), "min_approvals (3) exceeds the number of approver groups (2)"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := strings.Join(ValidateLaneAnalyzerBlock(tc.la, "appdb"), "; "); !strings.Contains(got, tc.want) {
				t.Errorf("ValidateLaneAnalyzerBlock = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMinApprovalsServedByCapability(t *testing.T) {
	if !slices.Contains(SidecarCapabilities(), CapabilityApproverMinApprovals) {
		t.Fatalf("header lacks %s", CapabilityApproverMinApprovals)
	}
	la := approverBlock(LevelApprovers{
		High: &LevelApprover{Type: analyzer.ApproverHuman, Owners: true, Groups: []string{"leads"}, AllGroupsMustApprove: true},
	})
	la.ApprovalRule = "dba-rule"
	cfg := *blockLane(la)

	approversOnly := Handshake{Version: "1.300.0", Capabilities: slices.DeleteFunc(SidecarCapabilities(),
		func(c string) bool { return c == CapabilityApproverMinApprovals })}
	served := ServeLevelApprovers(cfg, approversOnly)
	if err := CheckServable(served, approversOnly); err != nil {
		t.Errorf("build with approvers refused: %v", err)
	}
	if served.Listeners[0].Analyzer.Approvers.High.AllGroupsMustApprove {
		t.Error("all_groups_must_approve was not stripped")
	}

	old := Handshake{Version: "1.200.0", Capabilities: []string{"protocol:postgres"}}
	if err := CheckServable(ServeLevelApprovers(cfg, old), old); err == nil || !strings.Contains(err.Error(), CapabilityLevelApprovers) {
		t.Errorf("CheckServable = %v, want refusal naming %s", err, CapabilityLevelApprovers)
	}
}
