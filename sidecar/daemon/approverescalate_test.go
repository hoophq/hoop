package daemon

import (
	"slices"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
)

const escalateDoc = `{"listeners":[{"name":"appdb","protocol":"postgres","listen":":1","upstream":"h:1",
	"analyzer":{"high":"require_approval",
		"approvers":{
			"high":{"type":"human","owners":true,
				"escalate":{"after_sec":900,"groups":["risk","leads"]}}}}}]}`

func TestEscalateDecodesAndSurvivesServedForm(t *testing.T) {
	cfg, err := LoadConfigBytes([]byte(escalateDoc))
	if err != nil {
		t.Fatal(err)
	}
	cfg.ControlPlaneURL = "https://cp.example.com"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	got := cfg.Listeners[0].Analyzer.LevelApprover(analyzer.RiskHigh).Escalate
	if got == nil || got.AfterSec != 900 || !slices.Equal(got.Groups, []string{"risk", "leads"}) {
		t.Fatalf("escalate = %+v", got)
	}
	served := ServedForm(*cfg).Listeners[0].Analyzer.LevelApprover(analyzer.RiskHigh).Escalate
	if served == nil || served.AfterSec != 900 {
		t.Fatalf("served escalate = %+v", served)
	}
}

func TestEscalateRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		la   *LaneAnalyzerConfig
		want string
	}{
		"on the approval agent": {approverBlock(LevelApprovers{High: &LevelApprover{Type: analyzer.ApproverAIAgent,
			Escalate: &ApprovalEscalation{AfterSec: 600, Groups: []string{"risk"}}}}), "approvers.high: escalate names people nobody asks"},
		"delay too short": {approverBlock(LevelApprovers{High: &LevelApprover{Type: analyzer.ApproverHuman,
			Escalate: &ApprovalEscalation{AfterSec: 10, Groups: []string{"risk"}}}}), "approvers.high: escalate.after_sec must be between"},
		"delay too long": {approverBlock(LevelApprovers{High: &LevelApprover{Type: analyzer.ApproverHuman,
			Escalate: &ApprovalEscalation{AfterSec: MaxEscalationDelaySec + 1, Groups: []string{"risk"}}}}), "approvers.high: escalate.after_sec must be between"},
		"no groups": {approverBlock(LevelApprovers{High: &LevelApprover{Type: analyzer.ApproverHuman,
			Escalate: &ApprovalEscalation{AfterSec: 600}}}), "approvers.high: escalate.groups is required"},
		"blank group": {approverBlock(LevelApprovers{High: &LevelApprover{Type: analyzer.ApproverHuman,
			Escalate: &ApprovalEscalation{AfterSec: 600, Groups: []string{"risk", " "}}}}), "approvers.high: escalate.groups[1] is blank"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := strings.Join(ValidateLaneAnalyzerBlock(tc.la, "appdb"), "; "); !strings.Contains(got, tc.want) {
				t.Errorf("ValidateLaneAnalyzerBlock = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEscalateIsServedByWhatTheBuildDecodes(t *testing.T) {
	if !slices.Contains(SidecarCapabilities(), CapabilityApproverEscalate) {
		t.Fatalf("the header lacks %s", CapabilityApproverEscalate)
	}
	la := approverBlock(LevelApprovers{
		High: &LevelApprover{
			Type:     analyzer.ApproverHuman,
			Groups:   []string{"dba"},
			Escalate: &ApprovalEscalation{AfterSec: 900, Groups: []string{"risk"}},
		},
	})
	la.ApprovalRule = "dba-rule"
	cfg := *blockLane(la)

	current := Handshake{Version: "1.300.0", Capabilities: SidecarCapabilities()}
	if err := CheckServable(ServeLevelApprovers(cfg, current), current); err != nil {
		t.Errorf("current build refused: %v", err)
	}

	approversOnly := Handshake{Version: "1.300.0", Capabilities: slices.DeleteFunc(SidecarCapabilities(),
		func(c string) bool { return c == CapabilityApproverEscalate })}
	served := ServeLevelApprovers(cfg, approversOnly)
	if err := CheckServable(served, approversOnly); err != nil {
		t.Errorf("build with approvers refused: %v", err)
	}
	if served.Listeners[0].Analyzer.Approvers.High.Escalate != nil {
		t.Error("escalate was not stripped")
	}
	if cfg.Listeners[0].Analyzer.Approvers.High.Escalate == nil {
		t.Error("ServeLevelApprovers changed its input")
	}

	old := Handshake{Version: "1.200.0", Capabilities: []string{"protocol:postgres"}}
	if err := CheckServable(ServeLevelApprovers(cfg, old), old); err == nil || !strings.Contains(err.Error(), CapabilityLevelApprovers) {
		t.Errorf("CheckServable = %v, want refusal naming %s", err, CapabilityLevelApprovers)
	}
}
