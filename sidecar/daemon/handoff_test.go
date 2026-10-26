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

func handoffLevel(allowed *AllowedActions) RiskLevel {
	return RiskLevel{Action: "ai-handoff", AllowedActions: allowed}
}

// allowed_actions reads the list and the object form, and writes the object
// form only when require_approval carries a configuration.
func TestAllowedActionsDecodeAndEncodeBothForms(t *testing.T) {
	for name, tc := range map[string]struct {
		in, out  string
		actions  []analyzer.Action
		approval HandoffApproval
		holds    bool
	}{
		"list": {
			in:      `["allow","warn"]`,
			actions: []analyzer.Action{analyzer.ActionAllow, analyzer.ActionWarn},
		},
		"list with the review spelling": {
			in:      `["require_review","block"]`,
			actions: []analyzer.Action{analyzer.ActionBlock, analyzer.ActionRequireReview},
			holds:   true, approval: HandoffApproval{Approver: "ai_agent"},
		},
		"object with a configured hold": {
			in:      `{"warn":{},"require_approval":{"approver":"human","approval_groups":["payments-approvers"]}}`,
			actions: []analyzer.Action{analyzer.ActionWarn, analyzer.ActionRequireReview},
			holds:   true, approval: HandoffApproval{Approver: "human", ApprovalGroups: []string{"payments-approvers"}},
		},
		"object with an empty hold": {
			in:      `{"block":{},"require_approval":{}}`,
			out:     `["block","require_approval"]`,
			actions: []analyzer.Action{analyzer.ActionBlock, analyzer.ActionRequireReview},
			holds:   true, approval: HandoffApproval{Approver: "ai_agent"},
		},
		"object with an agent prompt": {
			in:      `{"require_approval":{"prompt":"Approve reads only."}}`,
			actions: []analyzer.Action{analyzer.ActionRequireReview},
			holds:   true, approval: HandoffApproval{Approver: "ai_agent", Prompt: "Approve reads only."},
		},
		"empty list": {
			in:      `[]`,
			actions: []analyzer.Action{analyzer.ActionBlock},
		},
	} {
		t.Run(name, func(t *testing.T) {
			doc := `{"action":"ai-handoff","prompt":"Prefer warn","allowed_actions":` + tc.in + `}`
			var r RiskLevel
			if err := json.Unmarshal([]byte(doc), &r); err != nil {
				t.Fatal(err)
			}
			if got := r.AllowedActions.Actions(); !slices.Equal(got, tc.actions) {
				t.Errorf("actions = %v, want %v", got, tc.actions)
			}
			ap, holds := r.AllowedActions.RequireApproval()
			if holds != tc.holds || ap.Approver != tc.approval.Approver || ap.Prompt != tc.approval.Prompt ||
				!slices.Equal(ap.ApprovalGroups, tc.approval.ApprovalGroups) {
				t.Errorf("require_approval = %+v %t, want %+v %t", ap, holds, tc.approval, tc.holds)
			}
			raw, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			want := tc.out
			if want == "" {
				want = tc.in
			}
			if wantDoc := `{"action":"ai-handoff","prompt":"Prefer warn","allowed_actions":` + want + `}`; string(raw) != wantDoc {
				t.Errorf("encoded %s\nwant    %s", raw, wantDoc)
			}
		})
	}

	// Absent: all four, and a hold goes to the approval agent.
	r := handoffLevel(nil)
	if got := r.AllowedActions.Actions(); len(got) != 4 {
		t.Errorf("absent allowed_actions = %v, want all four", got)
	}
	if ap, ok := r.AllowedActions.RequireApproval(); !ok || ap.Approver != "ai_agent" {
		t.Errorf("absent allowed_actions hold = %+v %t", ap, ok)
	}
	if raw, _ := json.Marshal(r); string(raw) != `"ai-handoff"` {
		t.Errorf("a bare hand-off serves %s", raw)
	}

	for name, in := range map[string]string{
		"unknown key in the hold": `{"require_approval":{"approvers":"human"}}`,
		"hold is not an object":   `{"require_approval":"human"}`,
		"a key twice":             `{"warn":{},"warn":{}}`,
		"a scalar":                `"allow"`,
		"two configured holds":    `{"require_approval":{"approver":"human"},"require_review":{"approver":"ai_agent"}}`,
	} {
		var r RiskLevel
		if err := json.Unmarshal([]byte(`{"action":"ai-handoff","allowed_actions":`+in+`}`), &r); err == nil {
			t.Errorf("%s: loaded", name)
		}
	}
}

// Each row of the ticket's warning table. Warned, never refused.
func TestEachHandoffSettingThatDoesNothingIsWarned(t *testing.T) {
	for name, tc := range map[string]struct {
		level string
		want  string
	}{
		"empty allowed_actions": {`{"action":"ai-handoff","allowed_actions":[]}`,
			"allowed_actions is empty, so the level falls back to block"},
		"ai-handoff in allowed_actions": {`{"action":"ai-handoff","allowed_actions":["warn","ai-handoff"]}`,
			`allowed_actions value "ai-handoff" is ignored`},
		"unknown value": {`{"action":"ai-handoff","allowed_actions":["warn","delete"]}`,
			`allowed_actions value "delete" is unknown and ignored`},
		"only ignored values": {`{"action":"ai-handoff","allowed_actions":["ai-handoff"]}`,
			"names no action the hand-off agent can select, so the level falls back to block"},
		"configuration inside warn": {`{"action":"ai-handoff","allowed_actions":{"warn":{"x":1},"block":{}}}`,
			"allowed_actions.warn carries a configuration, which is ignored"},
		"allowed_actions on another action": {`{"action":"block","allowed_actions":["warn"]}`,
			`sets allowed_actions, and its action "block" is not ai-handoff, so they are ignored`},
		"prompt on a level that holds nothing": {`{"action":"warn","prompt":"p"}`, "holds nothing"},
		"prompt on a human level": {`{"action":"require_approval","approver":"human","prompt":"p"}`,
			"prompt is ignored"},
		"approver beside ai-handoff": {`{"action":"ai-handoff","approver":"human"}`,
			"write them under allowed_actions.require_approval"},
		"prompt on a human hand-off hold": {`{"action":"ai-handoff","allowed_actions":{"require_approval":` +
			`{"approver":"human","prompt":"p"}}}`, "allowed_actions.require_approval prompt is ignored"},
	} {
		t.Run(name, func(t *testing.T) {
			la := laneBlock()
			if err := json.Unmarshal([]byte(tc.level), &la.MediumRisk); err != nil {
				t.Fatal(err)
			}
			if p := ValidateLaneAnalyzerBlock(la, "appdb"); len(p) != 0 {
				t.Fatalf("refused: %v", p)
			}
			if got := strings.Join(LaneAnalyzerWarnings(la), "\n"); !strings.Contains(got, tc.want) {
				t.Errorf("warnings %q, want %q", got, tc.want)
			}
		})
	}
	clean := laneBlock()
	clean.MediumRisk = RiskLevel{Action: "ai-handoff", Prompt: "p",
		AllowedActions: NewAllowedActions([]string{"warn", "block"}, HandoffApproval{Approver: "human"})}
	if w := LaneAnalyzerWarnings(clean); len(w) != 0 {
		t.Errorf("a clean hand-off warned: %v", w)
	}
}

// The warnings reach -validate's lane notes.
func TestValidateReportsTheHandoffWarnings(t *testing.T) {
	la := laneBlock()
	la.MediumRisk = handoffLevel(NewAllowedActions([]string{}, HandoffApproval{}))
	cfg := blockLane(la)
	cfg.Analyzer.Provider = "stub-tools"
	lanes, err := Validate(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(lanes[0].Notes, "\n"), "falls back to block") {
		t.Errorf("notes %v lack the warning", lanes[0].Notes)
	}
}

func init() {
	analyzer.Register("stub-tools", func(analyzer.Options) (analyzer.Provider, error) {
		return &handoffAgent{replies: []string{"warn"}}, nil
	})
}

// handoffAgent rates everything high and answers each Decide in order, the
// last reply repeating, as a ToolCaller.
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
	name := h.replies[min(n, len(h.replies)-1)]
	return []analyzer.ToolCall{{Name: name, Args: json.RawMessage(`{"reason":"checked"}`)}}, nil
}

// A hand-off level whose hold is human needs a plane or a terminal, like a
// human level; one the approval agent decides needs neither. The wait
// settings count it as a level a person approves.
func TestAHumanHandoffNeedsSomewhereToFile(t *testing.T) {
	deps := &analyzerDeps{cfg: &AnalyzerConfig{Provider: "stub", Model: "m"}, provider: &handoffAgent{replies: []string{"warn"}}}
	lane := func(allowed *AllowedActions) *Config {
		la := laneBlock()
		la.HighRisk = handoffLevel(allowed)
		return blockLane(la)
	}
	human := NewAllowedActions([]string{"warn"}, HandoffApproval{Approver: "human", ApprovalGroups: []string{"dba"}})
	if _, err := buildLanes(lane(human), nil, deps); err == nil || !strings.Contains(err.Error(), "in a terminal") {
		t.Fatalf("a human hand-off with nowhere to file built: %v", err)
	}
	withPlane := lane(human)
	withPlane.ControlPlaneURL = "https://cp.example.com"
	if _, err := buildLanes(withPlane, nil, deps); err != nil {
		t.Fatalf("a human hand-off under a plane was refused: %v", err)
	}
	for name, allowed := range map[string]*AllowedActions{
		"absent: ai_agent hold": nil,
		"agent hold":            NewAllowedActions([]string{"require_approval"}, HandoffApproval{Prompt: "p"}),
		"no hold":               NewAllowedActions([]string{"allow", "warn"}, HandoffApproval{}),
	} {
		if _, err := buildLanes(lane(allowed), nil, deps); err != nil {
			t.Errorf("%s: refused without a plane: %v", name, err)
		}
	}

	la := laneBlock()
	la.HighRisk, la.ReviewMode = handoffLevel(human), "return"
	if p := ValidateLaneAnalyzerBlock(la, "appdb"); len(p) != 0 {
		t.Errorf("review_mode beside a human hand-off was refused: %v", p)
	}
	la.HighRisk = handoffLevel(nil)
	if p := strings.Join(ValidateLaneAnalyzerBlock(la, "appdb"), "\n"); !strings.Contains(p, "with a human approver") {
		t.Errorf("review_mode beside an agent-only hand-off: %q", p)
	}
	if !analyzerHolds(la) || humanHolds(la) {
		t.Errorf("an agent hand-off: holds=%t human=%t", analyzerHolds(la), humanHolds(la))
	}

	// The provider must run the agent's tools.
	deps.provider = highRiskProvider{}
	if _, err := buildLanes(lane(nil), nil, deps); err == nil || !strings.Contains(err.Error(), "hand-off agent") {
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

// ai-handoff and allowed_actions are a capability: reported by this build,
// refused to one without it, with no downgrade.
func TestTheHandoffNeedsItsCapability(t *testing.T) {
	if !slices.Contains(SidecarCapabilities(), CapabilityAIHandoff) {
		t.Fatalf("the header lacks %s", CapabilityAIHandoff)
	}
	without := slices.DeleteFunc(slices.Clone(SidecarCapabilities()), func(c string) bool { return c == CapabilityAIHandoff })
	old := Handshake{Version: "1.200.0", Capabilities: []string{"protocol:postgres"}}
	rule := func(string) string { return "managed-rule" }
	for name, level := range map[string]RiskLevel{
		"bare hand-off":   handoffLevel(nil),
		"allowed_actions": handoffLevel(NewAllowedActions([]string{"warn"}, HandoffApproval{})),
	} {
		t.Run(name, func(t *testing.T) {
			la := laneBlock()
			la.MediumRisk = level
			cfg := *blockLane(la)
			for _, hs := range []Handshake{old, {Version: "1.200.0", Capabilities: without}} {
				if err := CheckServable(cfg, hs); err == nil || !strings.Contains(err.Error(), CapabilityAIHandoff) {
					t.Errorf("servable to a build without the capability: %v", err)
				}
				if _, err := DowngradeRiskLevels(cfg, hs, rule); err == nil || !strings.Contains(err.Error(), CapabilityAIHandoff) ||
					!strings.Contains(err.Error(), "medium risk") {
					t.Errorf("downgraded for a build without the capability: %v", err)
				}
			}
			full := Handshake{Version: "1.200.0", Capabilities: SidecarCapabilities()}
			if err := CheckServable(cfg, full); err != nil {
				t.Errorf("refused to this build: %v", err)
			}
			if out, err := DowngradeRiskLevels(cfg, full, rule); err != nil || out.Listeners[0].Analyzer != cfg.Listeners[0].Analyzer {
				t.Errorf("this build's document changed: %v", err)
			}
		})
	}
}

// A hold the hand-off agent selects files beside its level and approver the
// hand-off mark: a person's review, and the approval agent's decision.
func TestAHandoffHoldFilesTheHandoffMark(t *testing.T) {
	t.Run("human", func(t *testing.T) {
		cp, bodies := filingPlane(t, http.StatusCreated, `{"forward":false,"review":{"id":"r1","status":"REJECTED"}}`)
		deps := &analyzerDeps{cfg: &AnalyzerConfig{Provider: "stub", Model: "m"},
			provider: &handoffAgent{replies: []string{"require_approval"}}, cp: cp}
		la := laneBlock()
		la.HighRisk = handoffLevel(NewAllowedActions([]string{"warn"},
			HandoffApproval{Approver: "human", ApprovalGroups: []string{"payments-approvers"}}))
		pol, err := buildPolicy("payments", GuardrailsConfig{}, la, nil, nil, deps)
		if err != nil {
			t.Fatal(err)
		}
		v := pol.Evaluate(deleteFromUsers())
		if !v.Denied || len(*bodies) != 1 {
			t.Fatalf("verdict %+v, %d filings", v, len(*bodies))
		}
		body := (*bodies)[0]
		if body["risk_level"] != "high" || body["approver"] != "human" || body["handoff"] != true {
			t.Errorf("body = %v", body)
		}
		if _, ok := body["approval_rule"]; ok {
			t.Errorf("a hand-off hold named an approval_rule: %v", body)
		}
	})
	t.Run("ai_agent", func(t *testing.T) {
		cp, bodies := filingPlane(t, http.StatusCreated, `{"forward":false,"review":{"id":"a1","status":"APPROVED"}}`)
		agent := &handoffAgent{replies: []string{"require_approval", "approve"}}
		deps := &analyzerDeps{cfg: &AnalyzerConfig{Provider: "stub", Model: "m"}, provider: agent, cp: cp}
		la := laneBlock()
		la.HighRisk = handoffLevel(NewAllowedActions([]string{"block"}, HandoffApproval{Prompt: "Approve reads only."}))
		pol, err := buildPolicy("payments", GuardrailsConfig{}, la, nil, nil, deps)
		if err != nil {
			t.Fatal(err)
		}
		if v := pol.Evaluate(deleteFromUsers()); v.Denied {
			t.Fatalf("denied: %s", v.Message)
		}
		if len(agent.systems) != 2 || !strings.HasPrefix(agent.systems[1], "Approve reads only.") {
			t.Errorf("approval agent prompts %q", agent.systems)
		}
		body := (*bodies)[0]
		if body["risk_level"] != "high" || body["approver"] != "ai_agent" || body["decision"] != "approved" ||
			body["handoff"] != true {
			t.Errorf("body = %v", body)
		}
	})
	t.Run("a level outside a hand-off sends no mark", func(t *testing.T) {
		cp, bodies := filingPlane(t, http.StatusCreated, `{"forward":false,"review":{"id":"r1","status":"REJECTED"}}`)
		deps := &analyzerDeps{cfg: &AnalyzerConfig{Provider: "stub", Model: "m"}, provider: highRiskProvider{}, cp: cp}
		la := laneBlock()
		la.HighRisk = humanLevel("dba")
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
