package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/policy"
)

// handoffBlock hands high risk off with the allowed actions given.
func handoffBlock(allowed ...string) *LaneAnalyzerConfig {
	la := laneBlock()
	la.HighRisk = "ai-handoff"
	la.Handoffs = &LevelHandoffs{High: &LevelHandoff{AllowedActions: allowed}}
	return la
}

// The handoffs block decodes beside the string levels, strictly, and maps
// each ai-handoff level onto its hand-off, the approver from approvers.
func TestHandoffsDecode(t *testing.T) {
	doc := `{"listeners":[{"name":"appdb","protocol":"postgres","listen":":1","upstream":"h:1",
		"analyzer":{"high":"ai-handoff","medium":"ai-handoff","low":"ai-handoff","approval_rule":"dba-rule",
			"handoffs":{"high":{"allowed_actions":["allow","warn","block","require_approval"],"prompt":"Prefer warn"},
				"medium":{"allowed_actions":["warn","require_review"]},"low":{"allowed_actions":["allow"]}},
			"approvers":{"high":{"type":"human","groups":["dba"]},"medium":{"type":"ai_agent","prompt":"p"}}}}]}`
	cfg, err := LoadConfigBytes([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	la := cfg.Listeners[0].Analyzer
	if la.HighRisk != "ai-handoff" {
		t.Errorf("high = %q", la.HighRisk)
	}
	if h := la.LevelHandoff(analyzer.RiskHigh); h == nil || h.Prompt != "Prefer warn" || len(h.AllowedActions) != 4 {
		t.Errorf("high hand-off = %+v", h)
	}
	got := la.handoffs()
	want := map[analyzer.RiskLevel]analyzer.Handoff{
		analyzer.RiskHigh: {Prompt: "Prefer warn", Allowed: []analyzer.Action{analyzer.ActionAllow, analyzer.ActionWarn,
			analyzer.ActionBlock, analyzer.ActionRequireReview}, Approval: analyzer.Approval{Approver: analyzer.ApproverHuman}},
		analyzer.RiskMedium: {Allowed: []analyzer.Action{analyzer.ActionWarn, analyzer.ActionRequireReview},
			Approval: analyzer.Approval{Approver: analyzer.ApproverAIAgent, Prompt: "p"}},
		analyzer.RiskLow: {Allowed: []analyzer.Action{analyzer.ActionAllow}},
	}
	for level, w := range want {
		g := got[level]
		if g.Prompt != w.Prompt || !slices.Equal(g.Allowed, w.Allowed) || g.Approval != w.Approval {
			t.Errorf("%s hand-off = %+v, want %+v", level, g, w)
		}
	}
	if a := la.approvals(); len(a) != 0 {
		t.Errorf("approvals = %v, want none: the hand-off carries its approver", a)
	}
	if !analyzerHolds(la) || !humanHolds(la) || ruleHolds(la) {
		t.Errorf("holds=%t human=%t rule=%t", analyzerHolds(la), humanHolds(la), ruleHolds(la))
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}

	raw, err := json.Marshal(ServedForm(*cfg).Listeners[0].Analyzer)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"handoffs":{"high":{"allowed_actions":["allow","warn","block","require_approval"],"prompt":"Prefer warn"}`) {
		t.Errorf("served %s", raw)
	}

	for name, bad := range map[string]string{
		"map form":    `"low":{"allowed_actions":{"allow":{}}}`,
		"unknown key": `"low":{"allowed_actions":["allow"],"approver":"human"}`,
	} {
		in := strings.Replace(doc, `"low":{"allowed_actions":["allow"]}`, bad, 1)
		if _, err := LoadConfigBytes([]byte(in)); err == nil {
			t.Errorf("%s: loaded", name)
		}
	}
}

// Every hand-off the sidecar cannot act on is refused at load, by -validate
// and by the plane's validation.
func TestHandoffRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		la   func() *LaneAnalyzerConfig
		want string
	}{
		"no handoffs entry": {func() *LaneAnalyzerConfig {
			la := handoffBlock("warn")
			la.Handoffs = nil
			return la
		}, "high risk is ai-handoff and appdb: analyzer block: handoffs.high is missing"},
		"empty allowed_actions": {func() *LaneAnalyzerConfig { return handoffBlock() },
			"handoffs.high: allowed_actions is empty"},
		"unknown action": {func() *LaneAnalyzerConfig { return handoffBlock("warn", "delete") },
			`handoffs.high: allowed_actions[1] "delete" is not an action the hand-off agent can select`},
		"hand-off in allowed_actions": {func() *LaneAnalyzerConfig { return handoffBlock("ai-handoff") },
			`allowed_actions[0] "ai-handoff" is not an action`},
		"defer in allowed_actions": {func() *LaneAnalyzerConfig { return handoffBlock("defer") },
			`allowed_actions[0] "defer" is not an action`},
		"duplicate": {func() *LaneAnalyzerConfig { return handoffBlock("warn", "warn") },
			`allowed_actions[1] "warn" names warn twice`},
		"duplicate after folding": {func() *LaneAnalyzerConfig {
			return handoffBlock("require_approval", "require_review")
		}, `allowed_actions[1] "require_review" names require_approval twice`},
		"handoffs on another level": {func() *LaneAnalyzerConfig {
			la := handoffBlock("warn")
			la.Handoffs.Medium = &LevelHandoff{AllowedActions: []string{"warn"}}
			return la
		}, `handoffs.medium configures a hand-off, and the medium risk action "" is not ai-handoff`},
		"approver without a hold": {func() *LaneAnalyzerConfig {
			la := handoffBlock("allow", "warn")
			la.Approvers = &LevelApprovers{High: human("dba")}
			return la
		}, "approvers.high names an approver, and the high risk hand-off agent cannot select require_approval"},
		"bare hand-off hold without a rule": {func() *LaneAnalyzerConfig { return handoffBlock("warn", "require_approval") },
			"names no approval_rule"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := strings.Join(ValidateLaneAnalyzerBlock(tc.la(), "appdb"), "; "); !strings.Contains(got, tc.want) {
				t.Errorf("ValidateLaneAnalyzerBlock = %q, want %q", got, tc.want)
			}
			cfg := blockLane(tc.la())
			cfg.Analyzer.Provider = "stub-tools"
			cfg.ControlPlaneURL = "https://cp.example.com"
			if name == "bare hand-off hold without a rule" {
				// A local file decides that at build, where a terminal
				// may take the hold.
				if _, err := buildLanes(cfg, nil, &analyzerDeps{cfg: cfg.Analyzer,
					provider: &handoffAgent{replies: []string{"warn"}}, cp: &controlPlane{}}); err == nil ||
					!strings.Contains(err.Error(), "names no approval_rule") {
					t.Errorf("buildLanes = %v", err)
				}
				return
			}
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate = %v, want %q", err, tc.want)
			}
		})
	}

	// Accepted: a hold with an approver needs no rule, a hold with none
	// takes the rule, and both spellings of the hold read the same.
	for name, la := range map[string]*LaneAnalyzerConfig{
		"human approver": func() *LaneAnalyzerConfig {
			la := handoffBlock("warn", "require_approval")
			la.Approvers = &LevelApprovers{High: human("dba")}
			return la
		}(),
		"agent approver": func() *LaneAnalyzerConfig {
			la := handoffBlock("block", "require_review")
			la.Approvers = &LevelApprovers{High: agent("p")}
			return la
		}(),
		"rule": func() *LaneAnalyzerConfig {
			la := handoffBlock("warn", "require_approval")
			la.ApprovalRule = "dba-rule"
			return la
		}(),
		"no hold": handoffBlock("allow", "warn"),
	} {
		if got := ValidateLaneAnalyzerBlock(la, "appdb"); len(got) != 0 {
			t.Errorf("%s: refused: %v", name, got)
		}
	}

	// approval_rule beside a hand-off that cannot hold holds nothing.
	la := handoffBlock("allow", "warn")
	la.ApprovalRule = "dba-rule"
	if got := strings.Join(ValidateLaneAnalyzerBlock(la, "appdb"), "; "); !strings.Contains(got, "nothing on this lane would hold one") {
		t.Errorf("approval_rule beside a hand-off with no hold = %q", got)
	}
}

func init() {
	analyzer.Register("stub-tools", func(analyzer.Options) (analyzer.Provider, error) {
		return &handoffAgent{replies: []string{"warn"}}, nil
	})
}

// handoffAgent rates everything high and answers each Decide in order, the
// last reply repeating, as a ToolCaller. A reply is a tool name, or a tool
// name and its JSON arguments after a space.
type handoffAgent struct {
	highRiskProvider
	mu      sync.Mutex
	replies []string
	systems []string
}

func (h *handoffAgent) Decide(_ context.Context, system, _ string, _ []analyzer.ToolSpec) ([]analyzer.ToolCall, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := len(h.systems)
	h.systems = append(h.systems, system)
	reply := h.replies[min(n, len(h.replies)-1)]
	name, args, ok := strings.Cut(reply, " ")
	if !ok {
		args = `{"reason":"checked"}`
	}
	return []analyzer.ToolCall{{Name: name, Args: json.RawMessage(args)}}, nil
}

// A hand-off level whose hold a person approves needs a plane or a
// terminal, like a held level; one the approval agent decides, or one with
// no hold, needs neither. The wait settings count it as a level a person
// approves.
func TestAHumanHandoffNeedsSomewhereToFile(t *testing.T) {
	deps := &analyzerDeps{cfg: &AnalyzerConfig{Provider: "stub", Model: "m"}, provider: &handoffAgent{replies: []string{"warn"}}}
	withHuman := func() *LaneAnalyzerConfig {
		la := handoffBlock("warn", "require_approval")
		la.Approvers = &LevelApprovers{High: human("dba")}
		return la
	}
	if _, err := buildLanes(blockLane(withHuman()), nil, deps); err == nil || !strings.Contains(err.Error(), "in a terminal") {
		t.Fatalf("a human hand-off with nowhere to file built: %v", err)
	}
	withPlane := blockLane(withHuman())
	withPlane.ControlPlaneURL = "https://cp.example.com"
	if _, err := buildLanes(withPlane, nil, deps); err != nil {
		t.Fatalf("a human hand-off under a plane was refused: %v", err)
	}
	withAgent := handoffBlock("block", "require_approval")
	withAgent.Approvers = &LevelApprovers{High: agent("p")}
	for name, la := range map[string]*LaneAnalyzerConfig{
		"agent hold": withAgent,
		"no hold":    handoffBlock("allow", "warn"),
	} {
		if _, err := buildLanes(blockLane(la), nil, deps); err != nil {
			t.Errorf("%s: refused without a plane: %v", name, err)
		}
	}

	la := withHuman()
	la.ReviewMode = "return"
	if p := ValidateLaneAnalyzerBlock(la, "appdb"); len(p) != 0 {
		t.Errorf("review_mode beside a human hand-off was refused: %v", p)
	}
	withAgent.ReviewMode = "return"
	if p := strings.Join(ValidateLaneAnalyzerBlock(withAgent, "appdb"), "\n"); !strings.Contains(p, "with a human approver") {
		t.Errorf("review_mode beside an agent-only hand-off: %q", p)
	}
	if !analyzerHolds(withAgent) || humanHolds(withAgent) {
		t.Errorf("an agent hand-off: holds=%t human=%t", analyzerHolds(withAgent), humanHolds(withAgent))
	}

	// The provider must run the agent's tools.
	deps.provider = highRiskProvider{}
	if _, err := buildLanes(blockLane(handoffBlock("warn")), nil, deps); err == nil || !strings.Contains(err.Error(), "hand-off agent") {
		t.Fatalf("a hand-off built on a provider without tool calls: %v", err)
	}
}

// The deprecated rule form cannot hand off.
func TestTheRuleFormRefusesAHandoff(t *testing.T) {
	r := policy.Rule{Name: "risky", Type: policy.MatchAIAnalysis, HighRisk: "ai-handoff"}
	problems := strings.Join(validateLaneAnalysis([]policy.Rule{r}, nil, nil, nil, "appdb", ListenerConfig{}), "\n")
	if !strings.Contains(problems, "deprecated ai_analysis rule cannot do") {
		t.Errorf("problems %q", problems)
	}
}

// ai-handoff and handoffs are a capability: reported by this build, refused
// to one without it, with no downgrade.
func TestTheHandoffNeedsItsCapability(t *testing.T) {
	if !slices.Contains(SidecarCapabilities(), CapabilityAIHandoff) {
		t.Fatalf("the header lacks %s", CapabilityAIHandoff)
	}
	without := slices.DeleteFunc(slices.Clone(SidecarCapabilities()), func(c string) bool { return c == CapabilityAIHandoff })
	cfg := *blockLane(handoffBlock("warn"))
	for _, hs := range []Handshake{
		{Version: "1.200.0", Capabilities: []string{"protocol:postgres"}},
		{Version: "1.200.0", Capabilities: without},
	} {
		err := CheckServable(cfg, hs)
		if err == nil || !strings.Contains(err.Error(), CapabilityAIHandoff) ||
			!strings.Contains(err.Error(), "sets high risk to ai-handoff") || !strings.Contains(err.Error(), "handoffs") {
			t.Errorf("servable to a build without the capability: %v", err)
		}
	}
	if err := CheckServable(cfg, Handshake{Version: "1.200.0", Capabilities: SidecarCapabilities()}); err != nil {
		t.Errorf("refused to this build: %v", err)
	}

	// An empty handoffs block says nothing, and is not served.
	empty := laneBlock()
	empty.Handoffs = &LevelHandoffs{}
	if err := CheckServable(*blockLane(empty), Handshake{Version: "1.200.0", Capabilities: without}); err != nil {
		t.Errorf("an empty handoffs block was refused: %v", err)
	}
}

// A hold the hand-off agent selects files by its approver: a human level
// files the level and the hand-off mark, the rule path files the rule as it
// always did, and the approval agent decides in the sidecar and files
// nothing.
func TestAHandoffHoldFiling(t *testing.T) {
	const answer = `{"forward":false,"review":{"id":"r1","status":"REJECTED"}}`
	t.Run("human", func(t *testing.T) {
		cp, bodies := filingPlane(t, http.StatusCreated, answer)
		deps := &analyzerDeps{cfg: &AnalyzerConfig{Provider: "stub", Model: "m"},
			provider: &handoffAgent{replies: []string{"require_approval"}}, cp: cp}
		la := handoffBlock("warn", "require_approval")
		la.Approvers = &LevelApprovers{High: human("payments-approvers")}
		pol, err := buildPolicy("payments", GuardrailsConfig{}, la, nil, nil, deps)
		if err != nil {
			t.Fatal(err)
		}
		if v := pol.Evaluate(deleteFromUsers()); !v.Denied || len(*bodies) != 1 {
			t.Fatalf("verdict %+v, %d filings", v, len(*bodies))
		}
		body := (*bodies)[0]
		if body["listener_name"] != "payments" || body["risk_level"] != "high" || body["handoff"] != true {
			t.Errorf("body = %v", body)
		}
		for _, k := range []string{"approval_rule", "approver", "decision", "reason"} {
			if _, ok := body[k]; ok {
				t.Errorf("body sent %s: %v", k, body)
			}
		}
	})
	t.Run("rule", func(t *testing.T) {
		cp, bodies := filingPlane(t, http.StatusCreated, answer)
		deps := &analyzerDeps{cfg: &AnalyzerConfig{Provider: "stub", Model: "m"},
			provider: &handoffAgent{replies: []string{"require_approval"}}, cp: cp}
		la := handoffBlock("warn", "require_approval")
		la.ApprovalRule = "payments-approvers"
		pol, err := buildPolicy("payments", GuardrailsConfig{}, la, nil, nil, deps)
		if err != nil {
			t.Fatal(err)
		}
		if v := pol.Evaluate(deleteFromUsers()); !v.Denied || len(*bodies) != 1 {
			t.Fatalf("verdict %+v, %d filings", v, len(*bodies))
		}
		body := (*bodies)[0]
		if body["approval_rule"] != "payments-approvers" {
			t.Errorf("body = %v", body)
		}
		for _, k := range []string{"risk_level", "handoff", "approver"} {
			if _, ok := body[k]; ok {
				t.Errorf("body sent %s: %v", k, body)
			}
		}
	})
	t.Run("ai_agent", func(t *testing.T) {
		cp, bodies := filingPlane(t, http.StatusCreated, answer)
		agentStub := &handoffAgent{replies: []string{"require_approval",
			`approve {"reason_code":"in_scope","reason":"bounded"}`}}
		deps := &analyzerDeps{cfg: &AnalyzerConfig{Provider: "stub", Model: "m"}, provider: agentStub, cp: cp}
		la := handoffBlock("block", "require_approval")
		la.Approvers = &LevelApprovers{High: agent("Approve reads only.")}
		pol, err := buildPolicy("payments", GuardrailsConfig{}, la, nil, nil, deps)
		if err != nil {
			t.Fatal(err)
		}
		v := pol.Evaluate(deleteFromUsers())
		if v.Denied {
			t.Fatalf("denied: %s", v.Message)
		}
		if len(agentStub.systems) != 2 || !strings.HasPrefix(agentStub.systems[1], "Approve reads only.") {
			t.Errorf("approval agent prompts %q", agentStub.systems)
		}
		if len(*bodies) != 0 {
			t.Errorf("the approval agent's decision was filed: %v", *bodies)
		}
		if v.Annotations[analyzer.MetadataApprovalDecision] != "approved" ||
			v.Annotations[analyzer.MetadataApprovalReasonCode] != "in_scope" {
			t.Errorf("annotations = %v", v.Annotations)
		}
	})
	t.Run("a level outside a hand-off sends no mark", func(t *testing.T) {
		cp, bodies := filingPlane(t, http.StatusCreated, answer)
		deps := &analyzerDeps{cfg: &AnalyzerConfig{Provider: "stub", Model: "m"}, provider: highRiskProvider{}, cp: cp}
		la := approverBlock(LevelApprovers{High: human("dba"), Medium: human("dba")})
		pol, err := buildPolicy("payments", GuardrailsConfig{}, la, nil, nil, deps)
		if err != nil {
			t.Fatal(err)
		}
		pol.Evaluate(deleteFromUsers())
		if _, ok := (*bodies)[0]["handoff"]; ok {
			t.Errorf("body = %v", (*bodies)[0])
		}
	})
}

// The hand-off agent's reason reaches the record only after the detector;
// with no detector the record keeps no free text.
func TestTheHandoffReasonIsRedactedForTheRecord(t *testing.T) {
	for name, tc := range map[string]struct {
		det    Plugin
		reason string
		kept   bool
	}{
		"detector, send raw": {stubPlugin{entities: []string{"CREDIT_CARD"}, find: "4111111111111111"},
			"scoped to <CREDIT_CARD>", true},
		"no detector": {nil, "", false},
	} {
		t.Run(name, func(t *testing.T) {
			stub := &handoffAgent{replies: []string{`warn {"reason":"scoped to 4111111111111111"}`}}
			deps := &analyzerDeps{cfg: &AnalyzerConfig{Provider: "stub", Model: "m"}, provider: stub, det: tc.det}
			la := handoffBlock("warn", "block")
			la.Send = SendRaw
			pol, err := buildPolicy("payments", GuardrailsConfig{}, la, nil, nil, deps)
			if err != nil {
				t.Fatal(err)
			}
			v := pol.Evaluate(deleteFromUsers())
			got, kept := v.Annotations[analyzer.MetadataHandoffReason]
			if kept != tc.kept || got != tc.reason {
				t.Errorf("handoff_reason = %q (%t), want %q (%t)", got, kept, tc.reason, tc.kept)
			}
			if v.Annotations[analyzer.MetadataHandoffDecision] != "warn" {
				t.Errorf("annotations = %v", v.Annotations)
			}
		})
	}
}
