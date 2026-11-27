package daemon

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
)

// notified is a human level with static groups and notify.
func notified(notify map[string]NotifyRoute, groups ...string) *LevelApprover {
	return &LevelApprover{Type: analyzer.ApproverHuman, Groups: groups, Notify: notify}
}

const notifyDoc = `{"listeners":[{"name":"appdb","protocol":"postgres","listen":":1","upstream":"h:1",
	"analyzer":{"high":"require_approval","medium":"require_approval",
		"approvers":{
			"high":{"type":"human","groups":["dba"],
				"notify":{"slack":{"delivery":"both","channels_candidates":["C0123ABC"]}}},
			"medium":{"type":"human","notify":{"slack":{"delivery":"dm"}}}}}}]}`

// Notify decodes strictly beside groups, a level with no groups takes it
// too, and the served document carries it unchanged.
func TestNotifyDecodesBesideStaticGroups(t *testing.T) {
	cfg, err := LoadConfigBytes([]byte(notifyDoc))
	if err != nil {
		t.Fatal(err)
	}
	cfg.ControlPlaneURL = "https://cp.example.com"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	la := cfg.Listeners[0].Analyzer
	wantHigh := map[string]NotifyRoute{"slack": {Delivery: "both", ChannelsCandidates: []string{"C0123ABC"}}}
	if got := la.LevelApprover(analyzer.RiskHigh); !reflect.DeepEqual(got.Notify, wantHigh) ||
		!slices.Equal(got.Groups, []string{"dba"}) {
		t.Errorf("high = %+v", got)
	}
	if got := la.LevelApprover(analyzer.RiskMedium).Notify["slack"].EffectiveDelivery(); got != DeliveryDM {
		t.Errorf("medium delivery = %q, want dm", got)
	}
	// The sidecar files a notify level as it files any static level: no principal.
	if routed := la.routedLevels(); len(routed) != 0 {
		t.Errorf("routedLevels = %v, want none: notify is not routing", routed)
	}
	served := ServedForm(*cfg).Listeners[0].Analyzer
	if got := served.LevelApprover(analyzer.RiskHigh); !reflect.DeepEqual(got.Notify, wantHigh) {
		t.Errorf("served high = %+v", got)
	}

	unknown := strings.Replace(notifyDoc, `"delivery":"dm"`, `"delivery":"dm","channel":"C1"`, 1)
	if _, err := LoadConfigBytes([]byte(unknown)); err == nil {
		t.Error("an unknown notify key decoded")
	}
}

func TestNotifyRefusals(t *testing.T) {
	slackDM := map[string]NotifyRoute{"slack": {}}
	for name, tc := range map[string]struct {
		la   *LaneAnalyzerConfig
		want string
	}{
		"beside routing": {approverBlock(LevelApprovers{High: &LevelApprover{Type: analyzer.ApproverHuman,
			Routing: &ApprovalRouting{}, Notify: slackDM}}), "approvers.high: notify and routing are alternatives"},
		"on the approval agent": {approverBlock(LevelApprovers{Medium: &LevelApprover{Type: analyzer.ApproverAIAgent,
			Notify: slackDM}}), "approvers.medium: notify names people nobody asks"},
		"blank channel": {approverBlock(LevelApprovers{High: notified(map[string]NotifyRoute{"slack": {
			Delivery: "channel", ChannelsCandidates: []string{"C1", " "}}}, "dba")}),
			"approvers.high: notify.slack.channels_candidates[1] is blank"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := strings.Join(ValidateLaneAnalyzerBlock(tc.la, "appdb"), "; "); !strings.Contains(got, tc.want) {
				t.Errorf("ValidateLaneAnalyzerBlock = %q, want %q", got, tc.want)
			}
		})
	}
	ok := approverBlock(LevelApprovers{High: notified(slackDM, "dba"), Medium: notified(slackDM)})
	if got := ValidateLaneAnalyzerBlock(ok, "appdb"); len(got) != 0 {
		t.Errorf("notify beside groups, and with no groups, was refused: %v", got)
	}
}

// What the control plane ignores is a note, as under routing, and names the
// level's notify, not routing.notify.
func TestNotifyWarnings(t *testing.T) {
	la := approverBlock(LevelApprovers{
		High: notified(map[string]NotifyRoute{
			"slack": {Delivery: "page", ChannelsCandidates: []string{"C1"}},
			"email": {},
		}, "dba"),
		Medium: human("sre"),
	})
	got := LaneApproverWarnings(la)
	for _, want := range []string{
		"approvers.high.notify.email is ignored: only slack and teams are notify keys",
		`approvers.high.notify.slack.delivery "page" is not dm, channel or both; dm is used`,
		"approvers.high.notify.slack.channels_candidates is ignored: delivery dm posts to no channel",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("warnings = %q, want %q", got, want)
		}
	}
	if len(got) != 3 {
		t.Errorf("warnings = %q, want three", got)
	}
}

// The sidecar does nothing with notify, so a build that decodes approvers
// and not notify gets the document without it. A build that does not decode
// approvers is refused: under approval_rule the control plane posts in a
// channel.
func TestNotifyIsServedByWhatTheBuildDecodes(t *testing.T) {
	if !slices.Contains(SidecarCapabilities(), CapabilityApproverNotify) {
		t.Fatalf("the header lacks %s", CapabilityApproverNotify)
	}
	la := approverBlock(LevelApprovers{
		High:   notified(map[string]NotifyRoute{"slack": {Delivery: "dm"}}, "dba"),
		Medium: human("sre"),
	})
	la.ApprovalRule = "dba-rule"
	cfg := *blockLane(la)

	current := Handshake{Version: "1.300.0", Capabilities: SidecarCapabilities()}
	if out := ServeLevelApprovers(cfg, current); out.Listeners[0].Analyzer != cfg.Listeners[0].Analyzer {
		t.Error("a build with the capability was served a copy")
	}
	if err := CheckServable(ServeLevelApprovers(cfg, current), current); err != nil {
		t.Errorf("a current build was refused: %v", err)
	}

	approversOnly := Handshake{Version: "1.300.0", Capabilities: slices.DeleteFunc(SidecarCapabilities(),
		func(c string) bool { return c == CapabilityApproverNotify })}
	served := ServeLevelApprovers(cfg, approversOnly)
	if err := CheckServable(served, approversOnly); err != nil {
		t.Errorf("a build that decodes approvers was refused: %v", err)
	}
	got := served.Listeners[0].Analyzer.Approvers
	if got == nil || got.High == nil || got.High.Notify != nil || !slices.Equal(got.High.Groups, []string{"dba"}) ||
		got.Medium == nil || !slices.Equal(got.Medium.Groups, []string{"sre"}) {
		t.Errorf("served approvers = %+v, want the levels without notify", got)
	}
	if cfg.Listeners[0].Analyzer.Approvers.High.Notify == nil {
		t.Error("ServeLevelApprovers changed its input")
	}

	old := Handshake{Version: "1.200.0", Capabilities: []string{"protocol:postgres"}}
	kept := ServeLevelApprovers(cfg, old)
	if kept.Listeners[0].Analyzer.Approvers == nil {
		t.Fatal("approvers were dropped from a block with notify: the hold would post in a channel")
	}
	if err := CheckServable(kept, old); err == nil || !strings.Contains(err.Error(), CapabilityLevelApprovers) {
		t.Errorf("CheckServable = %v, want a refusal naming %s", err, CapabilityLevelApprovers)
	}
}
