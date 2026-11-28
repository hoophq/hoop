package daemon

import (
	"slices"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
)

func owned(groups ...string) *LevelApprover {
	return &LevelApprover{Type: analyzer.ApproverHuman, Owners: true, Groups: groups}
}

const ownersDoc = `{"listeners":[{"name":"appdb","protocol":"postgres","listen":":1","upstream":"h:1",
	"analyzer":{"high":"require_approval","medium":"require_approval",
		"approvers":{
			"high":{"type":"human","owners":true,"groups":["dba"],
				"notify":{"slack":{"delivery":"both","channels_candidates":["C0123ABC"]}}},
			"medium":{"type":"human","owners":true}}}}]}`

func TestOwnersDecodesAndSurvivesServedForm(t *testing.T) {
	cfg, err := LoadConfigBytes([]byte(ownersDoc))
	if err != nil {
		t.Fatal(err)
	}
	cfg.ControlPlaneURL = "https://cp.example.com"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	la := cfg.Listeners[0].Analyzer
	if got := la.LevelApprover(analyzer.RiskHigh); !got.Owners || !slices.Equal(got.Groups, []string{"dba"}) {
		t.Errorf("high = %+v", got)
	}
	if got := la.LevelApprover(analyzer.RiskMedium); !got.Owners || len(got.Groups) != 0 {
		t.Errorf("medium = %+v", got)
	}
	served := ServedForm(*cfg).Listeners[0].Analyzer
	if !served.LevelApprover(analyzer.RiskMedium).Owners {
		t.Error("served medium lost owners")
	}
}

func TestOwnersRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		la   *LaneAnalyzerConfig
		want string
	}{
		"beside routing": {approverBlock(LevelApprovers{High: &LevelApprover{Type: analyzer.ApproverHuman,
			Owners: true, Routing: &ApprovalRouting{}}}), "approvers.high: owners and routing are alternatives"},
		"on the approval agent": {approverBlock(LevelApprovers{Medium: &LevelApprover{Type: analyzer.ApproverAIAgent,
			Owners: true}}), "approvers.medium: owners name people nobody asks"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := strings.Join(ValidateLaneAnalyzerBlock(tc.la, "appdb"), "; "); !strings.Contains(got, tc.want) {
				t.Errorf("ValidateLaneAnalyzerBlock = %q, want %q", got, tc.want)
			}
		})
	}
}

// A build that decodes approvers and not owners is served the block without
// owners, and the plane still acts on its own copy. A build that does not
// decode approvers at all is refused: under approval_rule other groups would
// approve and a channel would get the statement.
func TestOwnersIsServedByWhatTheBuildDecodes(t *testing.T) {
	if !slices.Contains(SidecarCapabilities(), CapabilityApproverOwners) {
		t.Fatalf("the header lacks %s", CapabilityApproverOwners)
	}
	la := approverBlock(LevelApprovers{
		High:   owned("dba"),
		Medium: owned(),
	})
	la.ApprovalRule = "dba-rule"
	cfg := *blockLane(la)

	current := Handshake{Version: "1.300.0", Capabilities: SidecarCapabilities()}
	if err := CheckServable(ServeLevelApprovers(cfg, current), current); err != nil {
		t.Errorf("current build refused: %v", err)
	}

	approversOnly := Handshake{Version: "1.300.0", Capabilities: slices.DeleteFunc(SidecarCapabilities(),
		func(c string) bool { return c == CapabilityApproverOwners })}
	served := ServeLevelApprovers(cfg, approversOnly)
	if err := CheckServable(served, approversOnly); err != nil {
		t.Errorf("build with approvers was refused: %v", err)
	}
	got := served.Listeners[0].Analyzer.Approvers
	if got == nil || got.High == nil || got.High.Owners || !slices.Equal(got.High.Groups, []string{"dba"}) ||
		got.Medium == nil || got.Medium.Owners {
		t.Errorf("served approvers = %+v, want owners stripped", got)
	}
	if !cfg.Listeners[0].Analyzer.Approvers.High.Owners {
		t.Error("ServeLevelApprovers changed its input")
	}

	old := Handshake{Version: "1.200.0", Capabilities: []string{"protocol:postgres"}}
	kept := ServeLevelApprovers(cfg, old)
	if kept.Listeners[0].Analyzer.Approvers == nil {
		t.Fatal("approvers were dropped from a block with owners")
	}
	if err := CheckServable(kept, old); err == nil || !strings.Contains(err.Error(), CapabilityLevelApprovers) {
		t.Errorf("CheckServable = %v, want refusal naming %s", err, CapabilityLevelApprovers)
	}
}
