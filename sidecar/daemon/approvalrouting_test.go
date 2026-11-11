package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/policy"
)

// routed is a human approver with AI routing.
func routed(r ApprovalRouting) *LevelApprover {
	return &LevelApprover{Type: analyzer.ApproverHuman, Routing: &r}
}

const routingDoc = `{"listeners":[{"name":"appdb","protocol":"postgres","listen":":1","upstream":"h:1",
	"analyzer":{"high":"require_approval","medium":"require_approval",
		"approvers":{
			"high":{"type":"human","routing":{"candidates":["payments-approvers","sre"],
				"notify":{"slack":{"delivery":"both","channels_candidates":["C0123ABC"]},
					"teams":{"delivery":"channel","channels_candidates":["19:abc@thread.tacv2"]}}}},
			"medium":{"type":"human","groups":["sre"]}}}}]}`

// Routing decodes strictly beside groups, and the served document carries it
// unchanged: the plane reads it from what it served, like groups.
func TestRoutingSurvivesTheServedDocument(t *testing.T) {
	cfg, err := LoadConfigBytes([]byte(routingDoc))
	if err != nil {
		t.Fatal(err)
	}
	cfg.ControlPlaneURL = "https://cp.example.com"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	want := ApprovalRouting{
		Candidates: []string{"payments-approvers", "sre"},
		Notify: map[string]NotifyRoute{
			"slack": {Delivery: "both", ChannelsCandidates: []string{"C0123ABC"}},
			"teams": {Delivery: "channel", ChannelsCandidates: []string{"19:abc@thread.tacv2"}},
		},
	}
	la := cfg.Listeners[0].Analyzer
	if a := la.LevelApprover(analyzer.RiskHigh); a == nil || a.Routing == nil || !reflect.DeepEqual(*a.Routing, want) {
		t.Fatalf("high approver = %+v", a)
	}
	if got := LaneApproverWarnings(la); len(got) != 0 {
		t.Errorf("a clean routing was warned about: %v", got)
	}

	raw, err := json.Marshal(ServedForm(*cfg))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"routing":{"candidates":["payments-approvers","sre"],"notify":{`+
		`"slack":{"delivery":"both","channels_candidates":["C0123ABC"]},`+
		`"teams":{"delivery":"channel","channels_candidates":["19:abc@thread.tacv2"]}}}`) {
		t.Errorf("served %s", raw)
	}
	back, err := LoadConfigBytes(raw)
	if err != nil {
		t.Fatalf("the served document does not load: %v", err)
	}
	if !reflect.DeepEqual(back.Listeners[0].Analyzer.Approvers, la.Approvers) {
		t.Errorf("round trip = %+v, want %+v", back.Listeners[0].Analyzer.Approvers, la.Approvers)
	}

	// An empty routing still means AI routing over every group.
	empty := strings.Replace(routingDoc, `"medium":{"type":"human","groups":["sre"]}`,
		`"medium":{"type":"human","routing":{}}`, 1)
	cfg, err = LoadConfigBytes([]byte(empty))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(ServedForm(*cfg).Listeners[0].Analyzer)
	if !strings.Contains(string(raw), `"medium":{"type":"human","routing":{}}`) {
		t.Errorf("served %s, want the empty routing kept", raw)
	}
	cfg.ControlPlaneURL = "https://cp.example.com"
	if err := cfg.Validate(); err != nil {
		t.Errorf("an empty routing was refused: %v", err)
	}
	if a := cfg.Listeners[0].Analyzer.LevelApprover(analyzer.RiskMedium); a == nil || a.Routing == nil {
		t.Errorf("the empty routing was collapsed: %+v", a)
	}
	approversOnly := Handshake{Version: "1.300.0", Capabilities: slices.DeleteFunc(SidecarCapabilities(),
		func(c string) bool { return c == CapabilityApprovalRouting })}
	if err := CheckServable(*cfg, approversOnly); err == nil ||
		!strings.Contains(err.Error(), "approvers.medium.routing") {
		t.Errorf("CheckServable = %v, want the empty routing to need %s", err, CapabilityApprovalRouting)
	}

	// An unknown key inside routing is refused like any other.
	bad := strings.Replace(routingDoc, `"candidates":`, `"emails":["a@b.c"],"candidates":`, 1)
	if _, err := LoadConfigBytes([]byte(bad)); err == nil || !strings.Contains(err.Error(), "emails") {
		t.Errorf("an unknown routing key loaded: %v", err)
	}
}

// Each routing setting the plane ignores or reads as another value is a
// -validate note, said once, and never a refusal.
func TestEachRoutingWarningIsNotedOnce(t *testing.T) {
	la := laneBlock()
	la.HighRisk, la.MediumRisk = "require_approval", "require_approval"
	la.Approvers = &LevelApprovers{
		High: routed(ApprovalRouting{Notify: map[string]NotifyRoute{
			"email": {Delivery: "dm"},
			"slack": {Delivery: "sms"},
			"teams": {Delivery: "dm", ChannelsCandidates: []string{"19:abc@thread.tacv2"}},
		}}),
		Medium: &LevelApprover{Type: analyzer.ApproverAIAgent, Routing: &ApprovalRouting{Candidates: []string{"sre"}}},
	}
	cfg := blockLane(la)
	cfg.ControlPlaneURL = "https://cp.example.com"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("a routing warning refused the lane: %v", err)
	}
	want := []string{
		`approvers.high.routing.notify.email is ignored: only slack and teams notify`,
		`approvers.high.routing.notify.slack.delivery "sms" is not dm, channel or both; dm is used`,
		`approvers.high.routing.notify.teams.channels_candidates is ignored: delivery dm posts to no channel`,
		`approvers.medium.routing is ignored: the type is "ai_agent", and the approval agent notifies nobody`,
	}
	if got := LaneApproverWarnings(la); !slices.Equal(got, want) {
		t.Errorf("warnings =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	lanes, err := buildLanes(cfg, nil, &analyzerDeps{cfg: cfg.Analyzer, provider: approving()})
	if err != nil {
		t.Fatalf("buildLanes: %v", err)
	}
	for _, w := range want {
		if n := countOf(lanes[0].notes, w); n != 1 {
			t.Errorf("note %q appears %d times in %v", w, n, lanes[0].notes)
		}
	}

	// An empty delivery is dm: its channels are ignored, and it is no
	// delivery to warn about.
	la.Approvers = &LevelApprovers{High: routed(ApprovalRouting{Notify: map[string]NotifyRoute{
		"slack": {ChannelsCandidates: []string{"C0123ABC"}},
	}})}
	if got := LaneApproverWarnings(la); !slices.Equal(got, []string{
		`approvers.high.routing.notify.slack.channels_candidates is ignored: delivery dm posts to no channel`,
	}) {
		t.Errorf("warnings = %v", got)
	}
}

func countOf(notes []string, want string) int {
	n := 0
	for _, s := range notes {
		if s == want {
			n++
		}
	}
	return n
}

// Routing has no downgrade: static approvers would page other people. A
// build that reads approvers and not routing is refused naming the
// capability, and so is a build that reads neither, even beside an
// approval_rule that would take the other levels.
func TestRoutingIsRefusedToABuildWithoutIt(t *testing.T) {
	if !slices.Contains(SidecarCapabilities(), CapabilityApprovalRouting) {
		t.Fatalf("the header lacks %s", CapabilityApprovalRouting)
	}
	la := approverBlock(LevelApprovers{High: routed(ApprovalRouting{Candidates: []string{"sre"}}), Medium: human("dba")})
	la.ApprovalRule = "dba-rule"
	cfg := *blockLane(la)

	current := Handshake{Version: "1.300.0", Capabilities: SidecarCapabilities()}
	if err := CheckServable(ServeLevelApprovers(cfg, current), current); err != nil {
		t.Errorf("a current build was refused: %v", err)
	}

	approversOnly := Handshake{Version: "1.300.0", Capabilities: slices.DeleteFunc(SidecarCapabilities(),
		func(c string) bool { return c == CapabilityApprovalRouting })}
	served := ServeLevelApprovers(cfg, approversOnly)
	if !reflect.DeepEqual(served.Listeners[0].Analyzer, cfg.Listeners[0].Analyzer) {
		t.Errorf("a build with approvers was served %+v", served.Listeners[0].Analyzer)
	}
	err := CheckServable(served, approversOnly)
	if err == nil || !strings.Contains(err.Error(), CapabilityApprovalRouting) ||
		!strings.Contains(err.Error(), "approvers.high.routing") {
		t.Errorf("CheckServable = %v, want a refusal naming %s", err, CapabilityApprovalRouting)
	}

	old := Handshake{Version: "1.200.0", Capabilities: []string{"protocol:postgres"}}
	served = ServeLevelApprovers(cfg, old)
	if served.Listeners[0].Analyzer.Approvers == nil {
		t.Fatal("approvers with routing were dropped for an old build")
	}
	err = CheckServable(served, old)
	if err == nil || !strings.Contains(err.Error(), CapabilityApprovalRouting) ||
		!strings.Contains(err.Error(), CapabilityLevelApprovers) {
		t.Errorf("CheckServable = %v, want a refusal naming %s and %s", err,
			CapabilityApprovalRouting, CapabilityLevelApprovers)
	}
}

// A hand-off level that can select require_approval takes routing like a
// held level, and files the body it always sent: the plane reads the
// routing from the document it served.
func TestAHandoffLevelTakesRouting(t *testing.T) {
	la := handoffBlock("warn", "require_approval")
	la.Approvers = &LevelApprovers{High: routed(ApprovalRouting{Candidates: []string{"payments-approvers"},
		Notify: map[string]NotifyRoute{"teams": {Delivery: "both"}}})}
	cfg := blockLane(la)
	cfg.ControlPlaneURL = "https://cp.example.com"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("routing on a hand-off level was refused: %v", err)
	}
	if got := la.handoffs()[analyzer.RiskHigh].Approval; got != (analyzer.Approval{Approver: analyzer.ApproverHuman}) {
		t.Errorf("hand-off approval = %+v", got)
	}

	cp, bodies := filingPlane(t, http.StatusCreated, `{"forward":false,"review":{"id":"r1","status":"REJECTED"}}`)
	deps := &analyzerDeps{cfg: &AnalyzerConfig{Provider: "stub", Model: "m"},
		provider: &handoffAgent{replies: []string{"require_approval"}}, cp: cp}
	pol, err := buildPolicy("payments", GuardrailsConfig{}, la, nil, nil, deps)
	if err != nil {
		t.Fatal(err)
	}
	if v := pol.Evaluate(deleteFromUsers()); !v.Denied || len(*bodies) != 1 {
		t.Fatalf("verdict %+v, %d filings", v, len(*bodies))
	}
	keys := slices.Sorted(func(yield func(string) bool) {
		for k := range (*bodies)[0] {
			if !yield(k) {
				return
			}
		}
	})
	if !slices.Equal(keys, []string{"handoff", "listener_name", "payload", "risk_level"}) {
		t.Errorf("body keys = %v, want the hand-off filing unchanged", keys)
	}

	// A hand-off that cannot select require_approval holds nothing, with
	// routing or without.
	noHold := handoffBlock("warn", "block")
	noHold.Approvers = la.Approvers
	if got := strings.Join(ValidateLaneAnalyzerBlock(noHold, "appdb"), "; "); !strings.Contains(got,
		"approvers.high names an approver, and the high risk hand-off agent cannot select") {
		t.Errorf("ValidateLaneAnalyzerBlock = %q", got)
	}
}

// A routed level files the principal, so the plane can keep the requester
// out of the routing; a static level and an approval_rule filing never do.
// The hand-off hold sends it like a held level.
func TestOnlyARoutedLevelFilesThePrincipal(t *testing.T) {
	const answer = `{"forward":false,"review":{"id":"r1","status":"REJECTED"}}`
	long := strings.Repeat("é", 200)
	for name, tc := range map[string]struct {
		la        func() *LaneAnalyzerConfig
		provider  analyzer.Provider
		principal string
		want      any
	}{
		"routed level": {func() *LaneAnalyzerConfig {
			return approverBlock(LevelApprovers{High: routed(ApprovalRouting{}), Medium: human("dba")})
		}, highRiskProvider{}, "ana@corp", "ana@corp"},
		"routed hand-off level": {func() *LaneAnalyzerConfig {
			la := handoffBlock("warn", "require_approval")
			la.Approvers = &LevelApprovers{High: routed(ApprovalRouting{Candidates: []string{"sre"}})}
			return la
		}, &handoffAgent{replies: []string{"require_approval"}}, "ana@corp", "ana@corp"},
		"a long principal is bounded": {func() *LaneAnalyzerConfig {
			return approverBlock(LevelApprovers{High: routed(ApprovalRouting{}), Medium: human("dba")})
		}, highRiskProvider{}, long, long[:maxFiledPrincipal]},
		"no principal": {func() *LaneAnalyzerConfig {
			return approverBlock(LevelApprovers{High: routed(ApprovalRouting{}), Medium: human("dba")})
		}, highRiskProvider{}, "", nil},
		"static level": {func() *LaneAnalyzerConfig {
			return approverBlock(LevelApprovers{High: human("dba"), Medium: routed(ApprovalRouting{})})
		}, highRiskProvider{}, "ana@corp", nil},
		"approval_rule": {func() *LaneAnalyzerConfig {
			la := approverBlock(LevelApprovers{Medium: routed(ApprovalRouting{})})
			la.ApprovalRule = "payments-approvers"
			return la
		}, highRiskProvider{}, "ana@corp", nil},
	} {
		t.Run(name, func(t *testing.T) {
			cp, bodies := filingPlane(t, http.StatusCreated, answer)
			deps := &analyzerDeps{cfg: &AnalyzerConfig{Provider: "stub", Model: "m"}, provider: tc.provider, cp: cp}
			pol, err := buildPolicy("payments", GuardrailsConfig{}, tc.la(), nil, nil, deps)
			if err != nil {
				t.Fatal(err)
			}
			ec := &policy.EvalContext{Context: map[string]string{"principal": tc.principal}}
			if v := (policy.Chain{pol}).EvaluateWith(deleteFromUsers(), ec); !v.Denied || len(*bodies) != 1 {
				t.Fatalf("verdict %+v, %d filings", v, len(*bodies))
			}
			got, sent := (*bodies)[0]["principal"]
			if tc.want == nil && sent || tc.want != nil && got != tc.want {
				t.Errorf("principal = %v (sent %t), want %v", got, sent, tc.want)
			}
		})
	}
}

// A filing's HTTP budget is never shorter than the caller's: a timeout_sec
// above 30 s keeps its whole budget while the plane runs AI routing. With
// no deadline the backstop is reviewFilingTimeout; a claim keeps
// controlPlaneTimeout.
func TestAFilingKeepsALongerCallerBudget(t *testing.T) {
	var budgets []time.Duration
	prev := reviewClient
	t.Cleanup(func() { reviewClient = prev })
	reviewClient = func(timeout time.Duration) *http.Client {
		budgets = append(budgets, timeout)
		c := controlPlaneHTTPClient()
		c.Timeout = timeout
		return c
	}
	cp, _ := filingPlane(t, http.StatusCreated, `{"forward":false,"review":{"id":"r1","status":"PENDING"}}`)
	r := cp.reviewer("payments", "payments-approvers", false, nil)

	long, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	short, cancelShort := context.WithTimeout(context.Background(), time.Second)
	defer cancelShort()
	for _, ctx := range []context.Context{long, short, context.Background()} {
		if _, err := r.File(ctx, "DELETE FROM t"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.Claim(long, "r1"); err != nil {
		t.Fatal(err)
	}
	if len(budgets) != 4 {
		t.Fatalf("budgets = %v", budgets)
	}
	if budgets[0] <= 2*time.Minute-5*time.Second || budgets[0] > 2*time.Minute {
		t.Errorf("a two-minute filing had %v", budgets[0])
	}
	for i, want := range []time.Duration{reviewFilingTimeout, reviewFilingTimeout, controlPlaneTimeout} {
		if budgets[i+1] != want {
			t.Errorf("call %d had %v, want %v", i+1, budgets[i+1], want)
		}
	}
}
