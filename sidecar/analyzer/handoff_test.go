package analyzer_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

// decideReply is one answer of handoffProvider.
type decideReply struct {
	calls []analyzer.ToolCall
	err   error
}

type decideAsk struct {
	system, content string
	tools           []analyzer.ToolSpec
}

// handoffProvider classifies like stubProvider and answers each Decide in
// order, the last reply repeating.
type handoffProvider struct {
	stubProvider

	mu      sync.Mutex
	replies []decideReply
	delay   time.Duration
	asked   []decideAsk
}

func (p *handoffProvider) Decide(ctx context.Context, system, content string, tools []analyzer.ToolSpec) ([]analyzer.ToolCall, error) {
	p.mu.Lock()
	n := len(p.asked)
	p.asked = append(p.asked, decideAsk{system, content, tools})
	p.mu.Unlock()
	if p.delay > 0 {
		select {
		case <-time.After(p.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if len(p.replies) == 0 {
		return nil, nil
	}
	r := p.replies[min(n, len(p.replies)-1)]
	return r.calls, r.err
}

func (p *handoffProvider) asks() []decideAsk {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.asked)
}

func pick(tool, reason string) decideReply {
	return decideReply{calls: []analyzer.ToolCall{call(tool, `{"reason":"`+reason+`"}`)}}
}

var allFour = []analyzer.Action{analyzer.ActionAllow, analyzer.ActionWarn, analyzer.ActionBlock,
	analyzer.ActionRequireApproval}

// handoffEvaluator rates every delete medium and hands medium off. The
// record keeps the agents' reasons after redactSecret.
func handoffEvaluator(t *testing.T, p *handoffProvider, h analyzer.Handoff, edit func(*analyzer.Config)) *analyzer.Evaluator {
	t.Helper()
	p.level = analyzer.RiskMedium
	cfg := analyzer.Config{
		Rule:         "payments",
		Listener:     "payments",
		Provider:     p,
		Trigger:      deleteTrigger(),
		Actions:      analyzer.ActionMap{analyzer.RiskMedium: analyzer.ActionHandoff},
		Handoffs:     map[analyzer.RiskLevel]analyzer.Handoff{analyzer.RiskMedium: h},
		Timeout:      time.Second,
		RecordRedact: redactSecret,
	}
	if edit != nil {
		edit(&cfg)
	}
	return fastWait(mustNew(t, cfg))
}

func withCache(c *analyzer.Config) { c.CacheSize, c.CacheTTL = 16, time.Minute }

// handoffReviewer records what a hold filed: the level and the hand-off mark.
type handoffReviewer struct {
	mu      sync.Mutex
	levels  []analyzer.RiskLevel
	handoff []bool
}

func (h *handoffReviewer) File(ctx context.Context, _ string) (analyzer.ReviewResult, error) {
	l, _ := analyzer.FiledLevel(ctx)
	h.mu.Lock()
	h.levels = append(h.levels, l)
	h.handoff = append(h.handoff, analyzer.ViaHandoff(ctx))
	h.mu.Unlock()
	return analyzer.ReviewResult{ID: "r1", Status: "REJECTED"}, nil
}

func (h *handoffReviewer) Claim(context.Context, string) (analyzer.ReviewResult, error) {
	return analyzer.ReviewResult{}, errors.New("unused")
}

func (h *handoffReviewer) filed() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.levels)
}

func toolNames(tools []analyzer.ToolSpec) []string {
	var out []string
	for _, t := range tools {
		out = append(out, t.Name)
	}
	return out
}

// The agent gets one tool per allowed action and nothing else, the default
// prompt plus the fixed suffix, and the content the ticket lists.
func TestTheHandoffAgentGetsOnlyTheAllowedTools(t *testing.T) {
	for name, tc := range map[string]struct {
		allowed []analyzer.Action
		want    []string
	}{
		"all four":   {allFour, []string{"allow", "warn", "block", "require_approval"}},
		"warn block": {[]analyzer.Action{analyzer.ActionBlock, analyzer.ActionWarn}, []string{"warn", "block"}},
		"hold alias": {[]analyzer.Action{analyzer.ActionRequireReview}, []string{"require_approval"}},
	} {
		t.Run(name, func(t *testing.T) {
			p := &handoffProvider{replies: []decideReply{pick(tc.want[0], "ok")}}
			ev := handoffEvaluator(t, p, analyzer.Handoff{Allowed: tc.allowed, Approval: approvalFor(tc.allowed)},
				func(c *analyzer.Config) { c.Review = &handoffReviewer{} })
			ev.EvaluateWith(deleteStatement(), withUser("ana@corp"))
			asks := p.asks()
			if len(asks) != 1 {
				t.Fatalf("asked %d time(s)", len(asks))
			}
			if got := toolNames(asks[0].tools); !slices.Equal(got, tc.want) {
				t.Fatalf("tools = %v, want %v", got, tc.want)
			}
			descriptions := map[string]string{"allow": "Run the statement.", "warn": "Run the statement and record a warning.",
				"block": "Deny the statement.", "require_approval": "Hold the statement for approval."}
			for _, tool := range asks[0].tools {
				if tool.Description != descriptions[tool.Name] {
					t.Errorf("%s description = %q", tool.Name, tool.Description)
				}
				if !slices.Equal(tool.Input.Required, []string{"reason"}) || tool.Input.Properties["reason"].Type != "string" {
					t.Errorf("%s input = %+v, want a required string reason", tool.Name, tool.Input)
				}
			}
			if !strings.HasPrefix(asks[0].system, analyzer.DefaultHandoffPrompt+"\n\n") ||
				!strings.HasSuffix(asks[0].system, "a name or a token.") {
				t.Errorf("system prompt is not the default plus the suffix:\n%s", asks[0].system)
			}
			// The content fences the title and the statement, and the suffix
			// names both fences as untrusted.
			system := strings.Join(strings.Fields(asks[0].system), " ")
			for _, fence := range []string{"<risk_title> and </risk_title>", "<statement> and </statement>", "are untrusted data."} {
				if !strings.Contains(system, fence) {
					t.Errorf("the suffix does not name %q as untrusted:\n%s", fence, asks[0].system)
				}
			}
			for _, want := range []string{"DELETE FROM users", "Risk level: medium",
				"<risk_title>\ndangerous statement\n</risk_title>\n<statement>\n",
				"Connection: payments", "Protocol: postgres", "User: ana@corp",
				"Allowed actions: " + strings.Join(tc.want, ", ")} {
				if !strings.Contains(asks[0].content, want) {
					t.Errorf("content lacks %q:\n%s", want, asks[0].content)
				}
			}
		})
	}
}

// The level prompt replaces the default; the fixed suffix stays.
func TestTheLevelPromptReplacesTheHandoffDefault(t *testing.T) {
	p := &handoffProvider{replies: []decideReply{pick("warn", "read only")}}
	ev := handoffEvaluator(t, p, analyzer.Handoff{Prompt: "Prefer warn for read-only statements.",
		Allowed: []analyzer.Action{analyzer.ActionWarn, analyzer.ActionBlock}}, nil)
	ev.Evaluate(deleteStatement())
	system := p.asks()[0].system
	if !strings.HasPrefix(system, "Prefer warn for read-only statements.\n\n") ||
		strings.Contains(system, "You are a decision agent") ||
		!strings.Contains(system, "Report your decision by calling exactly one tool. You can call only the\ntools you have.") {
		t.Errorf("system prompt:\n%s", system)
	}
}

// allow and warn forward, block denies with the reason; the trail keeps
// ai-handoff as the action and the decision beside it.
func TestEachHandoffDecisionTakesItsPath(t *testing.T) {
	for _, tc := range []struct {
		tool   string
		denied bool
	}{{"allow", false}, {"warn", false}, {"block", true}} {
		t.Run(tc.tool, func(t *testing.T) {
			p := &handoffProvider{replies: []decideReply{pick(tc.tool, "scoped by id")}}
			ev := handoffEvaluator(t, p, analyzer.Handoff{Allowed: allFour}, nil)
			v := ev.Evaluate(deleteStatement())
			if v.Denied != tc.denied {
				t.Fatalf("denied = %t (%s)", v.Denied, v.Message)
			}
			for k, want := range map[string]string{
				analyzer.MetadataRiskLevel:       "medium",
				analyzer.MetadataAction:          "ai-handoff",
				analyzer.MetadataHandoffDecision: tc.tool,
				analyzer.MetadataHandoffReason:   "scoped by id",
			} {
				if got := v.Annotations[k]; got != want {
					t.Errorf("%s = %q, want %q", k, got, want)
				}
			}
			if tc.denied && v.Message != `dangerous statement: the hand-off agent blocked the statement: "scoped by id"` {
				t.Errorf("message = %q", v.Message)
			}
			if got := ev.Stats().Calls; got != 2 {
				t.Errorf("calls = %d, want the classification and the hand-off", got)
			}
		})
	}
}

// require_approval holds under the hand-off's human approver: the filing
// names the level and the hand-off.
func TestAHandoffHoldGoesToAPerson(t *testing.T) {
	rev := &handoffReviewer{}
	p := &handoffProvider{replies: []decideReply{pick("require_approval", "needs a person")}}
	ev := handoffEvaluator(t, p, analyzer.Handoff{Allowed: allFour,
		Approval: analyzer.Approval{Approver: analyzer.ApproverHuman}}, func(c *analyzer.Config) { c.Review = rev })
	v := ev.Evaluate(deleteStatement())
	if !v.Denied || !strings.Contains(v.Message, "rejected") {
		t.Fatalf("verdict %+v, want the rejected hold", v)
	}
	if len(rev.levels) != 1 || rev.levels[0] != analyzer.RiskMedium || !rev.handoff[0] {
		t.Fatalf("filed levels %v handoff %v", rev.levels, rev.handoff)
	}
	if v.Annotations[analyzer.MetadataHandoffDecision] != "require_approval" ||
		v.Annotations[analyzer.MetadataAction] != "ai-handoff" {
		t.Errorf("annotations = %v", v.Annotations)
	}
	if len(p.asks()) != 1 {
		t.Errorf("a human hold asked the model %d time(s)", len(p.asks()))
	}
}

// approve is the approval agent's approve call with a code and a reason.
func approve(reason string) decideReply {
	return decideReply{calls: []analyzer.ToolCall{call("approve",
		`{"reason_code":"in_scope","reason":"`+reason+`"}`)}}
}

// require_approval with the ai_agent approver runs the approval agent with
// the nested prompt, in the sidecar: its decision is recorded and nothing is
// filed.
func TestAHandoffHoldGoesToTheApprovalAgent(t *testing.T) {
	for name, tc := range map[string]struct {
		prompt string
		want   string
	}{
		"nested prompt":  {"Approve reads only.", "Approve reads only.\n\n"},
		"default prompt": {"", analyzer.DefaultApprovalPrompt + "\n\n"},
	} {
		t.Run(name, func(t *testing.T) {
			rev := &handoffReviewer{}
			p := &handoffProvider{replies: []decideReply{pick("require_approval", "needs a check"), approve("bounded")}}
			ev := handoffEvaluator(t, p, analyzer.Handoff{Allowed: allFour,
				Approval: analyzer.Approval{Approver: analyzer.ApproverAIAgent, Prompt: tc.prompt}},
				func(c *analyzer.Config) { c.Review = rev })
			v := ev.Evaluate(deleteStatement())
			if v.Denied {
				t.Fatalf("denied: %s", v.Message)
			}
			asks := p.asks()
			if len(asks) != 2 {
				t.Fatalf("asked %d time(s), want the hand-off and the approval", len(asks))
			}
			if !strings.HasPrefix(asks[1].system, tc.want) ||
				!strings.Contains(asks[1].system, "calling exactly one tool: approve or reject") {
				t.Errorf("approval system prompt:\n%s", asks[1].system)
			}
			if got := toolNames(asks[1].tools); !slices.Equal(got, []string{"approve", "reject"}) {
				t.Errorf("approval tools = %v", got)
			}
			for k, want := range map[string]string{
				analyzer.MetadataHandoffDecision:    "require_approval",
				analyzer.MetadataHandoffReason:      "needs a check",
				analyzer.MetadataApprover:           "ai_agent",
				analyzer.MetadataApprovalDecision:   "approved",
				analyzer.MetadataApprovalReasonCode: "in_scope",
				analyzer.MetadataApprovalReason:     "bounded",
			} {
				if got := v.Annotations[k]; got != want {
					t.Errorf("%s = %q, want %q", k, got, want)
				}
			}
			if rev.filed() != 0 {
				t.Errorf("filed %d review(s), want none", rev.filed())
			}
			if got := ev.Stats().Calls; got != 3 {
				t.Errorf("calls = %d, want classification, hand-off and approval", got)
			}
		})
	}
}

// A rejection of the nested approval agent denies with its code, never its
// prose; an observing lane does not call it.
func TestTheHandoffApprovalAgentRejectsAndSkipsObserve(t *testing.T) {
	p := &handoffProvider{replies: []decideReply{pick("require_approval", "check"),
		{calls: []analyzer.ToolCall{call("reject", `{"reason_code":"unbounded_scope","reason":"no where clause"}`)}}}}
	ev := handoffEvaluator(t, p, analyzer.Handoff{Allowed: allFour,
		Approval: analyzer.Approval{Approver: analyzer.ApproverAIAgent}}, nil)
	v := ev.Evaluate(deleteStatement())
	if !v.Denied || v.Message != "statement held for approval: the approval agent rejected the statement (unbounded_scope)" {
		t.Fatalf("verdict %+v", v)
	}
	if v.Annotations[analyzer.MetadataApprovalDecision] != "rejected" ||
		v.Annotations[analyzer.MetadataApprovalReasonCode] != "unbounded_scope" {
		t.Errorf("annotations = %v", v.Annotations)
	}

	p = &handoffProvider{replies: []decideReply{pick("require_approval", "check"), approve("ok")}}
	ev = handoffEvaluator(t, p, analyzer.Handoff{Allowed: allFour,
		Approval: analyzer.Approval{Approver: analyzer.ApproverAIAgent}},
		func(c *analyzer.Config) { c.Observing = true })
	v = ev.Evaluate(deleteStatement())
	if !v.Denied || !strings.Contains(v.Message, "the approval agent is not consulted in observe mode") {
		t.Fatalf("observing verdict %+v", v)
	}
	if len(p.asks()) != 1 {
		t.Errorf("asked %d time(s), want the hand-off agent only", len(p.asks()))
	}
}

// require_approval on a level with no approver files under the lane's
// approval rule: no level is filed, and the hold is marked as a hand-off.
func TestAHandoffHoldWithNoApproverFilesUnderTheRule(t *testing.T) {
	rev := &handoffReviewer{}
	p := &handoffProvider{replies: []decideReply{pick("require_approval", "needs a person")}}
	ev := handoffEvaluator(t, p, analyzer.Handoff{Allowed: allFour}, func(c *analyzer.Config) { c.Review = rev })
	if v := ev.Evaluate(deleteStatement()); !v.Denied {
		t.Fatalf("forwarded: %+v", v)
	}
	if len(rev.levels) != 1 || rev.levels[0] != "" || !rev.handoff[0] {
		t.Fatalf("filed levels %q handoff %v, want one rule filing", rev.levels, rev.handoff)
	}
}

// The agent's reason reaches the record and a block's denial only after the
// record redactor. With no redactor neither keeps it; a fail-safe's fixed
// reason stays.
func TestTheHandoffReasonIsRedactedForTheRecord(t *testing.T) {
	const secret = "card 4111111111111111"
	p := &handoffProvider{replies: []decideReply{pick("block", secret)}}
	v := handoffEvaluator(t, p, analyzer.Handoff{Allowed: allFour}, nil).Evaluate(deleteStatement())
	if got := v.Annotations[analyzer.MetadataHandoffReason]; got != "card <CREDIT_CARD>" {
		t.Errorf("reason = %q", got)
	}
	if strings.Contains(v.Message, "4111") || !strings.Contains(v.Message, "<CREDIT_CARD>") {
		t.Errorf("message = %q", v.Message)
	}

	p = &handoffProvider{replies: []decideReply{pick("block", secret)}}
	v = handoffEvaluator(t, p, analyzer.Handoff{Allowed: allFour},
		func(c *analyzer.Config) { c.RecordRedact = nil }).Evaluate(deleteStatement())
	if _, ok := v.Annotations[analyzer.MetadataHandoffReason]; ok {
		t.Errorf("no redactor kept the reason: %v", v.Annotations)
	}
	if v.Message != "dangerous statement: the hand-off agent blocked the statement" ||
		v.Annotations[analyzer.MetadataHandoffDecision] != "block" {
		t.Errorf("message = %q, annotations = %v", v.Message, v.Annotations)
	}

	p = &handoffProvider{}
	v = handoffEvaluator(t, p, analyzer.Handoff{Allowed: allFour},
		func(c *analyzer.Config) { c.RecordRedact = nil }).Evaluate(deleteStatement())
	if got := v.Annotations[analyzer.MetadataHandoffReason]; got != "the hand-off agent did not decide" {
		t.Errorf("fail-safe reason = %q", got)
	}
}

// A value that crosses the length bound is redacted whole: redaction runs
// on the full reason before the cut, and a cache hit repeats the redacted
// text, not the model's.
func TestAHandoffReasonIsRedactedBeforeItIsCut(t *testing.T) {
	reason := strings.Repeat("a", 1009) + " 4111111111111111"
	p := &handoffProvider{replies: []decideReply{pick("allow", reason)}}
	ev := handoffEvaluator(t, p, analyzer.Handoff{Allowed: allFour}, withCache)
	for _, name := range []string{"model answer", "cache hit"} {
		v := ev.Evaluate(deleteStatement())
		got := v.Annotations[analyzer.MetadataHandoffReason]
		if strings.Contains(got, "4111") || !strings.Contains(got, "<CREDIT_CARD>") {
			t.Errorf("%s: reason keeps the card digits: %q", name, got[max(0, len(got)-40):])
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.asked) != 1 {
		t.Errorf("%d model calls, want 1 (the second evaluation is a cache hit)", len(p.asked))
	}
}

// Anything but one valid call of an allowed tool applies the most
// restrictive allowed action, with the fixed reason, block allowed or not.
func TestAnUndecidedHandoffAppliesTheMostRestrictiveAction(t *testing.T) {
	failures := map[string]*handoffProvider{
		"no call":          {},
		"two calls":        {replies: []decideReply{{calls: []analyzer.ToolCall{call("allow", `{"reason":"a"}`), call("allow", `{"reason":"b"}`)}}}},
		"tool not allowed": {replies: []decideReply{pick("ai-handoff", "again")}},
		"unknown tool":     {replies: []decideReply{pick("approve", "a")}},
		"missing reason":   {replies: []decideReply{{calls: []analyzer.ToolCall{call("allow", `{}`)}}}},
		"blank reason":     {replies: []decideReply{pick("allow", "  ")}},
		"no arguments":     {replies: []decideReply{{calls: []analyzer.ToolCall{call("allow", "")}}}},
		"reason not text":  {replies: []decideReply{{calls: []analyzer.ToolCall{call("allow", `{"reason":7}`)}}}},
		"refusal":          {replies: []decideReply{{err: errors.New("model refused to answer")}}},
		"provider error":   {replies: []decideReply{{err: errors.New("503")}}},
		"timeout":          {delay: 5 * time.Second, replies: []decideReply{pick("allow", "late")}},
	}
	for _, set := range []struct {
		name    string
		allowed []analyzer.Action
		want    string
	}{
		{"all four", allFour, "block"},
		{"no block", []analyzer.Action{analyzer.ActionAllow, analyzer.ActionWarn, analyzer.ActionRequireApproval}, "require_approval"},
		{"allow and warn", []analyzer.Action{analyzer.ActionWarn, analyzer.ActionAllow}, "warn"},
		{"allow only", []analyzer.Action{analyzer.ActionAllow}, "allow"},
	} {
		for name, proto := range failures {
			t.Run(set.name+"/"+name, func(t *testing.T) {
				p := &handoffProvider{replies: proto.replies, delay: proto.delay}
				// A not-allowed known tool, for the sets that lack it.
				if name == "tool not allowed" && !slices.Contains(set.allowed, analyzer.ActionBlock) {
					p.replies = []decideReply{pick("block", "no")}
				}
				rev := &handoffReviewer{}
				ev := handoffEvaluator(t, p, analyzer.Handoff{Allowed: set.allowed,
					Approval: approvalFor(set.allowed)}, func(c *analyzer.Config) {
					c.Timeout = 50 * time.Millisecond
					c.Review = rev
				})
				v := ev.Evaluate(deleteStatement())
				assertFailSafe(t, v, rev, set.want, "the hand-off agent did not decide")
			})
		}
	}
}

// approvalFor names a human approver when the set allows a hold.
func approvalFor(allowed []analyzer.Action) analyzer.Approval {
	for _, a := range allowed {
		if a.Canonical() == analyzer.ActionRequireReview {
			return analyzer.Approval{Approver: analyzer.ApproverHuman}
		}
	}
	return analyzer.Approval{}
}

func assertFailSafe(t *testing.T, v policy.Verdict, rev *handoffReviewer, want, reason string) {
	t.Helper()
	if got := v.Annotations[analyzer.MetadataHandoffDecision]; got != want {
		t.Fatalf("decision = %q, want %q (%+v)", got, want, v)
	}
	if got := v.Annotations[analyzer.MetadataHandoffReason]; got != reason {
		t.Errorf("reason = %q, want %q", got, reason)
	}
	switch want {
	case "block":
		if !v.Denied || !strings.Contains(v.Message, reason+", so the statement is blocked") {
			t.Errorf("denied=%t message=%q", v.Denied, v.Message)
		}
	case "require_approval":
		if rev.filed() != 1 || !v.Denied {
			t.Errorf("filed %d hold(s), denied=%t", rev.filed(), v.Denied)
		}
	default:
		if v.Denied || rev.filed() != 0 {
			t.Errorf("denied=%t (%s), filed %d", v.Denied, v.Message, rev.filed())
		}
	}
}

// A provider reply that refuses beside a valid call decides nothing.
func TestARefusalBesideAHandoffCallFailsSafe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"stop_reason":"refusal","content":[{"type":"tool_use","name":"allow","input":{"reason":"ok"}}]}`)
	}))
	defer srv.Close()
	p, err := analyzer.NewProvider("anthropic", analyzer.Options{
		Endpoint: srv.URL, Model: "m", Credential: analyzer.NewSecret([]byte("k")),
	})
	if err != nil {
		t.Fatal(err)
	}
	rd := &realDecider{stubProvider: stubProvider{level: analyzer.RiskMedium}, decider: p.(analyzer.ToolCaller)}
	ev := mustNew(t, analyzer.Config{
		Rule: "payments", Listener: "payments", Provider: rd, Trigger: deleteTrigger(),
		Actions:  analyzer.ActionMap{analyzer.RiskMedium: analyzer.ActionHandoff},
		Handoffs: map[analyzer.RiskLevel]analyzer.Handoff{analyzer.RiskMedium: {Allowed: []analyzer.Action{analyzer.ActionAllow, analyzer.ActionWarn}}},
	})
	assertFailSafe(t, ev.Evaluate(deleteStatement()), &handoffReviewer{}, "warn", "the hand-off agent did not decide")
}

// The hand-off call is charged to max_calls: with one call left after the
// classification it is not made, and the level fails safe.
func TestTheHandoffCallCountsAgainstMaxCalls(t *testing.T) {
	for _, set := range []struct {
		allowed []analyzer.Action
		want    string
	}{
		{allFour, "block"},
		{[]analyzer.Action{analyzer.ActionAllow, analyzer.ActionWarn}, "warn"},
	} {
		p := &handoffProvider{replies: []decideReply{pick("allow", "ok")}}
		rev := &handoffReviewer{}
		ev := handoffEvaluator(t, p, analyzer.Handoff{Allowed: set.allowed}, func(c *analyzer.Config) {
			c.MaxCalls = 1
			c.Review = rev
		})
		assertFailSafe(t, ev.Evaluate(deleteStatement()), rev, set.want, "the hand-off agent did not decide")
		if len(p.asks()) != 0 || ev.Stats().Calls != 1 {
			t.Errorf("asked %d time(s), %d call(s), past the budget", len(p.asks()), ev.Stats().Calls)
		}
	}

	p := &handoffProvider{replies: []decideReply{pick("allow", "ok")}}
	ev := handoffEvaluator(t, p, analyzer.Handoff{Allowed: allFour}, func(c *analyzer.Config) { c.MaxCalls = 2 })
	if v := ev.Evaluate(deleteStatement()); v.Denied || ev.Stats().Calls != 2 {
		t.Fatalf("denied=%t calls=%d, want the classification and the hand-off", v.Denied, ev.Stats().Calls)
	}
}

// The agent decides only on a statement it reads in full: past
// max_input_bytes or with a cut body, no call is made and the level fails
// safe, also when a decision for the shape is cached.
func TestAStatementTheHandoffAgentCannotReadFailsSafe(t *testing.T) {
	const overInput = "the statement is longer than max_input_bytes, so the hand-off agent cannot read all of it"
	long := sqlStmt("DELETE FROM users WHERE email = '"+strings.Repeat("x", 300)+"'", inspect.OpDelete, "users")
	for _, set := range []struct {
		allowed []analyzer.Action
		want    string
	}{
		{allFour, "block"},
		{[]analyzer.Action{analyzer.ActionAllow, analyzer.ActionWarn}, "warn"},
	} {
		p := &handoffProvider{replies: []decideReply{pick("allow", "bounded")}}
		rev := &handoffReviewer{}
		ev := handoffEvaluator(t, p, analyzer.Handoff{Allowed: set.allowed}, func(c *analyzer.Config) {
			c.MaxInputBytes = 64
			c.Review = rev
			withCache(c)
		})
		// The short statement of the same shape is decided and cached.
		if v := ev.Evaluate(deleteStatement()); v.Annotations[analyzer.MetadataHandoffDecision] != "allow" {
			t.Fatalf("the short statement: %+v", v)
		}
		assertFailSafe(t, ev.Evaluate(long), rev, set.want, overInput)
		if len(p.asks()) != 1 {
			t.Errorf("asked %d time(s), want only for the short statement", len(p.asks()))
		}
	}

	p := &handoffProvider{replies: []decideReply{pick("allow", "ok")}}
	ev := handoffEvaluator(t, p, analyzer.Handoff{Allowed: allFour}, func(c *analyzer.Config) {
		c.Trigger = analyzer.Trigger{All: true}
	})
	v := ev.Evaluate(inspect.Statement{Protocol: inspect.HTTP, Direction: inspect.FromClient,
		Text: "POST /pay", Operation: inspect.OpInsert,
		HTTP: &inspect.HTTPDetail{Method: "POST", Target: "/pay", Body: `{"a":`, BodyTruncated: true}})
	assertFailSafe(t, v, &handoffReviewer{}, "block",
		"the request body is larger than the capture limit, so the hand-off agent cannot read all of it")
	if len(p.asks()) != 0 {
		t.Errorf("asked %d time(s) about a cut body", len(p.asks()))
	}
}

// A cached decision is reused with its reason and no call for the same
// statement; a literal variant reuses only the classification. A hold the
// decision selects still runs for each statement.
func TestTheHandoffDecisionIsCached(t *testing.T) {
	p := &handoffProvider{replies: []decideReply{pick("allow", "bounded by email")}}
	ev := handoffEvaluator(t, p, analyzer.Handoff{Allowed: allFour}, withCache)
	ev.Evaluate(deleteStatement())
	v := ev.Evaluate(deleteStatement())
	if v.Denied || v.Annotations[analyzer.MetadataAIStatus] != analyzer.StatusCached ||
		v.Annotations[analyzer.MetadataHandoffDecision] != "allow" ||
		v.Annotations[analyzer.MetadataHandoffReason] != "bounded by email" {
		t.Fatalf("cached verdict %+v", v)
	}
	if len(p.asks()) != 1 || p.calls.Load() != 1 {
		t.Errorf("asked %d time(s), classified %d time(s), want one each", len(p.asks()), p.calls.Load())
	}
	ev.Evaluate(sqlStmt("DELETE FROM users WHERE email = 'z@y.x'", inspect.OpDelete, "users"))
	if len(p.asks()) != 2 || p.calls.Load() != 1 {
		t.Errorf("literal variant: asked %d time(s), classified %d time(s), want 2 and 1",
			len(p.asks()), p.calls.Load())
	}

	rev := &handoffReviewer{}
	p = &handoffProvider{replies: []decideReply{pick("require_approval", "check")}}
	ev = handoffEvaluator(t, p, analyzer.Handoff{Allowed: allFour,
		Approval: analyzer.Approval{Approver: analyzer.ApproverHuman}}, func(c *analyzer.Config) {
		withCache(c)
		c.Review = rev
	})
	ev.Evaluate(deleteStatement())
	ev.Evaluate(deleteStatement())
	if len(p.asks()) != 1 || rev.filed() != 2 {
		t.Errorf("asked %d time(s), filed %d hold(s); want one decision and a hold per statement",
			len(p.asks()), rev.filed())
	}
}

// A decision is kept under the hand-off configuration it was made with: a
// cache that outlives a change of prompt, allowed set or approver misses.
func TestAHandoffConfigChangeMissesTheCache(t *testing.T) {
	agentAt := func(prompt string) analyzer.Approval {
		return analyzer.Approval{Approver: analyzer.ApproverAIAgent, Prompt: prompt}
	}
	base := analyzer.Handoff{Prompt: "p", Allowed: allFour, Approval: agentAt("a")}
	for name, changed := range map[string]analyzer.Handoff{
		"same":            base,
		"prompt":          {Prompt: "q", Allowed: allFour, Approval: agentAt("a")},
		"allowed":         {Prompt: "p", Allowed: allFour[:3]},
		"approver":        {Prompt: "p", Allowed: allFour, Approval: analyzer.Approval{Approver: analyzer.ApproverHuman}},
		"rule":            {Prompt: "p", Allowed: allFour},
		"approval prompt": {Prompt: "p", Allowed: allFour, Approval: agentAt("b")},
	} {
		t.Run(name, func(t *testing.T) {
			p := &handoffProvider{replies: []decideReply{pick("allow", "ok")}}
			first := handoffEvaluator(t, p, base, withCache)
			second := handoffEvaluator(t, p, changed, withCache)
			analyzer.ShareCache(second, first)
			first.Evaluate(deleteStatement())
			v := second.Evaluate(deleteStatement())
			if v.Annotations[analyzer.MetadataAIStatus] != analyzer.StatusCached {
				t.Fatalf("the classification missed: %v", v.Annotations)
			}
			want := 2
			if name == "same" {
				want = 1
			}
			if got := len(p.asks()); got != want {
				t.Errorf("asked %d time(s), want %d", got, want)
			}
		})
	}
}

// A fail-safe result is not cached: the next statement of the shape asks
// again.
func TestAnUndecidedHandoffIsNotCached(t *testing.T) {
	p := &handoffProvider{replies: []decideReply{{}, pick("allow", "ok")}}
	ev := handoffEvaluator(t, p, analyzer.Handoff{Allowed: allFour}, withCache)
	if v := ev.Evaluate(deleteStatement()); !v.Denied {
		t.Fatalf("an undecided hand-off forwarded: %+v", v)
	}
	v := ev.Evaluate(deleteStatement())
	if v.Denied || v.Annotations[analyzer.MetadataHandoffDecision] != "allow" || len(p.asks()) != 2 {
		t.Fatalf("second verdict %+v after %d ask(s)", v, len(p.asks()))
	}
}

// New refuses a hand-off it cannot run.
func TestNewRefusesAHandoffItCannotRun(t *testing.T) {
	handoff := analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionHandoff}
	for name, cfg := range map[string]analyzer.Config{
		"configuration on another action": {Provider: &handoffProvider{},
			Actions:  analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionBlock},
			Handoffs: map[analyzer.RiskLevel]analyzer.Handoff{analyzer.RiskHigh: {Allowed: allFour}}},
		"provider without tool calls": {Provider: &stubProvider{}, Actions: handoff,
			Handoffs: map[analyzer.RiskLevel]analyzer.Handoff{analyzer.RiskHigh: {Allowed: allFour}}},
		"provider without tool calls, no configuration": {Provider: &stubProvider{}, Actions: handoff},
		"an empty allowed list": {Provider: &handoffProvider{}, Actions: handoff,
			Handoffs: map[analyzer.RiskLevel]analyzer.Handoff{analyzer.RiskHigh: {Allowed: []analyzer.Action{}}}},
		"hand-off allowed": {Provider: &handoffProvider{}, Actions: handoff,
			Handoffs: map[analyzer.RiskLevel]analyzer.Handoff{analyzer.RiskHigh: {Allowed: []analyzer.Action{analyzer.ActionHandoff}}}},
		"unknown approver": {Provider: &handoffProvider{}, Actions: handoff,
			Handoffs: map[analyzer.RiskLevel]analyzer.Handoff{analyzer.RiskHigh: {Allowed: allFour,
				Approval: analyzer.Approval{Approver: "robot"}}}},
		"approver without a hold": {Provider: &handoffProvider{}, Actions: handoff,
			Handoffs: map[analyzer.RiskLevel]analyzer.Handoff{analyzer.RiskHigh: {Allowed: allFour[:2],
				Approval: analyzer.Approval{Approver: analyzer.ApproverHuman}}}},
		"approval prompt without an approver": {Provider: &handoffProvider{}, Actions: handoff,
			Handoffs: map[analyzer.RiskLevel]analyzer.Handoff{analyzer.RiskHigh: {Allowed: allFour,
				Approval: analyzer.Approval{Prompt: "p"}}}},
	} {
		if _, err := analyzer.New(cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// A hand-off with no Allowed list, and an ai-handoff level with no Handoffs
// entry, offer the agent all four tools; no entry also takes the default
// prompt.
func TestAnAbsentAllowedListOffersAllFour(t *testing.T) {
	for name, h := range map[string]*analyzer.Handoff{
		"nil allowed": {Prompt: "Prefer warn."},
		"no entry":    nil,
	} {
		t.Run(name, func(t *testing.T) {
			p := &handoffProvider{replies: []decideReply{pick("warn", "ok")}}
			ev := handoffEvaluator(t, p, analyzer.Handoff{}, func(c *analyzer.Config) {
				c.Review = &handoffReviewer{}
				if h == nil {
					c.Handoffs = nil
				} else {
					c.Handoffs[analyzer.RiskMedium] = *h
				}
			})
			if v := ev.Evaluate(deleteStatement()); v.Annotations[analyzer.MetadataHandoffDecision] != "warn" {
				t.Fatalf("verdict %+v", v)
			}
			asks := p.asks()
			if len(asks) != 1 {
				t.Fatalf("asked %d time(s)", len(asks))
			}
			if got := toolNames(asks[0].tools); !slices.Equal(got, []string{"allow", "warn", "block", "require_approval"}) {
				t.Errorf("tools = %v, want all four", got)
			}
			wantPrompt := analyzer.DefaultHandoffPrompt
			if h != nil {
				wantPrompt = h.Prompt
			}
			if !strings.HasPrefix(asks[0].system, wantPrompt+"\n\n") {
				t.Errorf("system prompt:\n%s", asks[0].system)
			}
		})
	}
}

// A level whose agent can select a hold makes the lane one that holds: a
// failed classification denies whatever FailOpen says.
func TestAHandoffThatCanHoldIgnoresFailOpen(t *testing.T) {
	for _, tc := range []struct {
		allowed []analyzer.Action
		denied  bool
	}{
		{allFour, true},
		{[]analyzer.Action{analyzer.ActionAllow, analyzer.ActionWarn}, false},
	} {
		p := &handoffProvider{stubProvider: stubProvider{err: errors.New("503")}}
		ev := handoffEvaluator(t, p, analyzer.Handoff{Allowed: tc.allowed}, func(c *analyzer.Config) { c.FailOpen = true })
		if v := ev.Evaluate(deleteStatement()); v.Denied != tc.denied {
			t.Errorf("allowed %v: denied = %t (%s), want %t", tc.allowed, v.Denied, v.Message, tc.denied)
		}
	}
}

// The classification is cached on the shape, but a hand-off decision is
// reused only for the inputs the agent read: another principal or another
// database asks again, the same ones hit.
func TestAHandoffDecisionIsKeyedOnWhatTheAgentRead(t *testing.T) {
	stmt := func(db string) inspect.Statement {
		s := deleteStatement()
		s.Database = db
		return s
	}
	p := &handoffProvider{replies: []decideReply{pick("allow", "own rows")}}
	ev := handoffEvaluator(t, p, analyzer.Handoff{Allowed: allFour}, withCache)

	ev.EvaluateWith(stmt("payments"), withUser("ana@corp"))
	steps := []struct {
		name, user, db string
		asks           int
	}{
		{"same principal and database", "ana@corp", "payments", 1},
		{"another principal", "bob@corp", "payments", 2},
		{"another database", "ana@corp", "ledger", 3},
		{"the first again", "ana@corp", "payments", 3},
		{"the second again", "bob@corp", "payments", 3},
	}
	asked := 1
	for _, s := range steps {
		v := ev.EvaluateWith(stmt(s.db), withUser(s.user))
		if v.Annotations[analyzer.MetadataAIStatus] != analyzer.StatusCached ||
			v.Annotations[analyzer.MetadataHandoffDecision] != "allow" {
			t.Fatalf("%s: %v", s.name, v.Annotations)
		}
		asks := p.asks()
		if len(asks) != s.asks {
			t.Fatalf("%s: asked %d time(s), want %d", s.name, len(asks), s.asks)
		}
		// A new call reads this principal and database.
		if last := asks[len(asks)-1].content; len(asks) > asked &&
			(!strings.Contains(last, "User: "+s.user) || !strings.Contains(last, "Database: "+s.db)) {
			t.Errorf("%s: the new call read %q", s.name, last)
		}
		asked = len(asks)
	}
	if p.calls.Load() != 1 {
		t.Errorf("classified %d time(s), want one: the shape cache stays shape-only", p.calls.Load())
	}
}

// The shape key turns a quoted identifier into "?", so two deletes that
// filter on different columns share a classification. The hand-off decision
// for one must not reach the other.
func TestAHandoffDecisionIsNotReusedForAnotherIdentifier(t *testing.T) {
	p := &handoffProvider{replies: []decideReply{pick("allow", "public column")}}
	ev := handoffEvaluator(t, p, analyzer.Handoff{Allowed: allFour}, withCache)

	ev.EvaluateWith(sqlStmt(`DELETE FROM users WHERE "id" = 1`, inspect.OpDelete, "users"), withUser("ana@corp"))
	v := ev.EvaluateWith(sqlStmt(`DELETE FROM users WHERE "ssn" = 1`, inspect.OpDelete, "users"), withUser("ana@corp"))
	if v.Annotations[analyzer.MetadataAIStatus] != analyzer.StatusCached {
		t.Fatalf("the second statement was not classified from the shape cache: %v", v.Annotations)
	}
	asks := p.asks()
	if len(asks) != 2 {
		t.Fatalf("asked the hand-off agent %d time(s), want 2", len(asks))
	}
	if !strings.Contains(asks[1].content, `"ssn"`) {
		t.Errorf("the second call did not read the second statement: %q", asks[1].content)
	}
	// The same statement again is a hit.
	ev.EvaluateWith(sqlStmt(`DELETE FROM users WHERE "ssn" = 1`, inspect.OpDelete, "users"), withUser("ana@corp"))
	if n := len(p.asks()); n != 2 {
		t.Errorf("the same statement asked again: %d call(s), want 2", n)
	}
}

// pinReviewer files pending requests r1, r2, ... in order and answers each
// claim from claim, which a test may use to act in the middle of one. It
// keeps what it filed and what it was asked to claim.
type pinReviewer struct {
	mu      sync.Mutex
	filed   []string
	claimed []string
	claim   func(id string) (analyzer.ReviewResult, error)
}

func (r *pinReviewer) File(_ context.Context, statement string) (analyzer.ReviewResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.filed = append(r.filed, statement)
	return analyzer.ReviewResult{ID: "r" + strconv.Itoa(len(r.filed)), Status: "PENDING"}, nil
}

func (r *pinReviewer) Claim(_ context.Context, id string) (analyzer.ReviewResult, error) {
	r.mu.Lock()
	r.claimed = append(r.claimed, id)
	claim := r.claim
	r.mu.Unlock()
	if claim == nil {
		return analyzer.ReviewResult{}, errors.New("no claim scripted")
	}
	return claim(id)
}

func (r *pinReviewer) counts() (filed, claimed []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.filed), slices.Clone(r.claimed)
}

// pinEvaluator hands medium off to an agent that may hold for a person, in
// return mode.
func pinEvaluator(t *testing.T, p *handoffProvider, rev *pinReviewer) *analyzer.Evaluator {
	return handoffEvaluator(t, p, analyzer.Handoff{Allowed: allFour,
		Approval: analyzer.Approval{Approver: analyzer.ApproverHuman}}, func(c *analyzer.Config) {
		c.Review = rev
		returnMode(c)
	})
}

func answer(id, status string, forward bool) func(string) (analyzer.ReviewResult, error) {
	return func(string) (analyzer.ReviewResult, error) {
		return analyzer.ReviewResult{ID: id, Status: status, Forward: forward}, nil
	}
}

// testClock is a clock a test moves by hand.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// The resend of a statement whose hold the agent selected claims the pinned
// request: still pending, it denies again with that id, with no model call
// and no new filing, even with no verdict cache and an agent that would now
// allow. The pin lives at most 30 minutes; after it the agent decides again.
func TestAPendingPinnedRequestHoldsTheResend(t *testing.T) {
	p := &handoffProvider{replies: []decideReply{pick("require_approval", "needs a person"), pick("allow", "fine now")}}
	rev := &pinReviewer{claim: answer("r1", "PENDING", false)}
	clock := &testClock{now: time.Unix(1_700_000_000, 0)}
	ev := pinEvaluator(t, p, rev)
	analyzer.SetHandoffPinClock(ev, clock.Now)

	if v := ev.Evaluate(deleteStatement()); !v.Denied || v.Annotations[analyzer.MetadataReviewID] != "r1" {
		t.Fatalf("first: %+v, want the pending r1", v)
	}
	for i := range 2 {
		clock.Add(10 * time.Minute)
		v := ev.Evaluate(deleteStatement())
		if !v.Denied || v.Annotations[analyzer.MetadataReviewID] != "r1" ||
			v.Annotations[analyzer.MetadataHandoffDecision] != "require_approval" ||
			v.Annotations[analyzer.MetadataHandoffReason] != "needs a person" ||
			!strings.Contains(v.Message, "r1") {
			t.Fatalf("resend %d: %+v, want r1 still pending", i+1, v)
		}
	}
	filed, claimed := rev.counts()
	if len(p.asks()) != 1 || len(filed) != 1 || !slices.Equal(claimed, []string{"r1", "r1"}) {
		t.Errorf("asked %d, filed %v, claimed %v; want 1 ask, 1 filing, 2 claims of r1", len(p.asks()), filed, claimed)
	}

	// 31 minutes after the filing the pin is gone: the agent decides again.
	clock.Add(11 * time.Minute)
	if v := ev.Evaluate(deleteStatement()); v.Denied || v.Annotations[analyzer.MetadataHandoffDecision] != "allow" {
		t.Fatalf("after the pin expired: %+v", v)
	}
	if _, claimed := rev.counts(); len(p.asks()) != 2 || len(claimed) != 2 {
		t.Errorf("after expiry: asked %d, claimed %v", len(p.asks()), claimed)
	}
}

// An approved pinned request releases the resend with its id, and the pin
// goes: the next statement asks the agent again and claims nothing.
func TestAnApprovedPinnedRequestReleasesTheResend(t *testing.T) {
	p := &handoffProvider{replies: []decideReply{pick("require_approval", "needs a person"), pick("allow", "fine")}}
	rev := &pinReviewer{claim: answer("r1", "EXECUTED", true)}
	ev := pinEvaluator(t, p, rev)
	ev.Evaluate(deleteStatement())
	v := ev.Evaluate(deleteStatement())
	if v.Denied || v.Annotations[analyzer.MetadataReviewID] != "r1" || len(p.asks()) != 1 {
		t.Fatalf("approved resend: %+v after %d ask(s), want r1 released", v, len(p.asks()))
	}
	v = ev.Evaluate(deleteStatement())
	filed, claimed := rev.counts()
	if v.Annotations[analyzer.MetadataHandoffDecision] != "allow" || len(p.asks()) != 2 ||
		len(filed) != 1 || len(claimed) != 1 {
		t.Fatalf("after release: %+v, asked %d, filed %v, claimed %v", v, len(p.asks()), filed, claimed)
	}
}

// A pinned request that reached a final status between resends (rejected,
// revoked, expired, spent by an accepted reservation, or gone) drops the pin
// and decides again: an agent that allows forwards and files nothing; one
// that holds again files a fresh request and pins it.
func TestAFinalPinnedRequestAsksTheAgentAgain(t *testing.T) {
	notFound := func(string) (analyzer.ReviewResult, error) {
		return analyzer.ReviewResult{}, fmt.Errorf("%w: r1", analyzer.ErrReviewNotFound)
	}
	for name, claim := range map[string]func(string) (analyzer.ReviewResult, error){
		"rejected": answer("r1", "REJECTED", false),
		"revoked":  answer("r1", "REVOKED", false),
		"expired":  answer("r1", "EXPIRED", false),
		"executed": answer("r1", "EXECUTED", false),
		"gone":     notFound,
	} {
		t.Run(name+"/allow", func(t *testing.T) {
			p := &handoffProvider{replies: []decideReply{pick("require_approval", "needs a person"), pick("allow", "fine")}}
			rev := &pinReviewer{claim: claim}
			ev := pinEvaluator(t, p, rev)
			ev.Evaluate(deleteStatement())
			v := ev.Evaluate(deleteStatement())
			if v.Denied || v.Annotations[analyzer.MetadataHandoffDecision] != "allow" ||
				v.Annotations[analyzer.MetadataReviewID] != "" || v.Annotations[analyzer.MetadataReviewMode] != "" {
				t.Fatalf("resend: %+v, want the agent's new allow", v)
			}
			ev.Evaluate(deleteStatement())
			filed, claimed := rev.counts()
			if len(p.asks()) != 3 || len(filed) != 1 || !slices.Equal(claimed, []string{"r1"}) {
				t.Errorf("asked %d, filed %v, claimed %v; want 3 asks, 1 filing, one claim of r1",
					len(p.asks()), filed, claimed)
			}
		})
		t.Run(name+"/hold again", func(t *testing.T) {
			p := &handoffProvider{replies: []decideReply{pick("require_approval", "needs a person")}}
			rev := &pinReviewer{claim: func(id string) (analyzer.ReviewResult, error) {
				if id == "r1" {
					return claim(id)
				}
				return analyzer.ReviewResult{ID: id, Status: "PENDING"}, nil
			}}
			ev := pinEvaluator(t, p, rev)
			ev.Evaluate(deleteStatement())
			if v := ev.Evaluate(deleteStatement()); !v.Denied || v.Annotations[analyzer.MetadataReviewID] != "r2" {
				t.Fatalf("resend: %+v, want a fresh pending r2", v)
			}
			ev.Evaluate(deleteStatement())
			if _, claimed := rev.counts(); !slices.Equal(claimed, []string{"r1", "r2"}) {
				t.Errorf("claimed %v, want r1 then the new pin r2", claimed)
			}
		})
	}
}

// A claim that fails denies, and keeps the pin for the next resend.
func TestAFailedPinnedClaimDenies(t *testing.T) {
	p := &handoffProvider{replies: []decideReply{pick("require_approval", "needs a person"), pick("allow", "fine")}}
	rev := &pinReviewer{claim: func(string) (analyzer.ReviewResult, error) {
		return analyzer.ReviewResult{}, errors.New("control plane unreachable")
	}}
	ev := pinEvaluator(t, p, rev)
	ev.Evaluate(deleteStatement())
	v := ev.Evaluate(deleteStatement())
	if !v.Denied || v.Err == nil || !strings.Contains(v.Message, "could not be checked") {
		t.Fatalf("resend: %+v, want a denial", v)
	}
	ev.Evaluate(deleteStatement())
	filed, claimed := rev.counts()
	if len(p.asks()) != 1 || len(filed) != 1 || !slices.Equal(claimed, []string{"r1", "r1"}) {
		t.Errorf("asked %d, filed %v, claimed %v; want the pin kept", len(p.asks()), filed, claimed)
	}
}

// Two resends race on one pin. A claims r1 and spends it; before A's answer
// lands, B reads r1 as used, decides again and files r2. A's cleanup must
// not delete B's pin: the next resend claims r2.
func TestANewerFilingOwnsThePin(t *testing.T) {
	p := &handoffProvider{replies: []decideReply{pick("require_approval", "needs a person")}}
	rev := &pinReviewer{}
	ev := pinEvaluator(t, p, rev)
	ev.Evaluate(deleteStatement())

	r1Claims := 0
	var b policy.Verdict
	rev.claim = func(id string) (analyzer.ReviewResult, error) {
		if id != "r1" {
			return analyzer.ReviewResult{ID: id, Status: "PENDING"}, nil
		}
		r1Claims++
		if r1Claims == 1 {
			// A's claim is in flight: B resends now.
			b = ev.Evaluate(deleteStatement())
			return analyzer.ReviewResult{ID: "r1", Status: "EXECUTED", Forward: true}, nil
		}
		return analyzer.ReviewResult{ID: "r1", Status: "EXECUTED"}, nil
	}
	if a := ev.Evaluate(deleteStatement()); a.Denied || a.Annotations[analyzer.MetadataReviewID] != "r1" {
		t.Fatalf("A: %+v, want released on r1", a)
	}
	if !b.Denied || b.Annotations[analyzer.MetadataReviewID] != "r2" {
		t.Fatalf("B: %+v, want a pending r2", b)
	}
	if c := ev.Evaluate(deleteStatement()); !c.Denied || c.Annotations[analyzer.MetadataReviewID] != "r2" {
		t.Fatalf("C: %+v, want r2 from B's pin", c)
	}
	if _, claimed := rev.counts(); !slices.Equal(claimed, []string{"r1", "r1", "r2"}) {
		t.Errorf("claimed %v, want A and B on r1, then C on r2", claimed)
	}
}

// A fail-safe hold files without a pin: the next statement asks again.
func TestAFailSafeHandoffHoldIsNotPinned(t *testing.T) {
	allowed := []analyzer.Action{analyzer.ActionAllow, analyzer.ActionRequireApproval}
	p := &handoffProvider{replies: []decideReply{{}, pick("allow", "fine")}}
	rev := &pinReviewer{claim: answer("r1", "PENDING", false)}
	ev := handoffEvaluator(t, p, analyzer.Handoff{Allowed: allowed, Approval: approvalFor(allowed)},
		func(c *analyzer.Config) {
			c.Review = rev
			returnMode(c)
		})
	if v := ev.Evaluate(deleteStatement()); v.Annotations[analyzer.MetadataHandoffReason] != "the hand-off agent did not decide" {
		t.Fatalf("fail-safe: %+v", v)
	}
	v := ev.Evaluate(deleteStatement())
	filed, claimed := rev.counts()
	if v.Annotations[analyzer.MetadataHandoffDecision] != "allow" || len(p.asks()) != 2 ||
		len(filed) != 1 || len(claimed) != 0 {
		t.Fatalf("after a fail-safe hold: %+v, asked %d, filed %v, claimed %v", v, len(p.asks()), filed, claimed)
	}
}

// The outcome of a hand-off level reports the action it resolved to, the
// agent's or the fail-safe, never ai-handoff; a cached decision reports the
// same.
func TestAHandoffOutcomeReportsTheResolvedAction(t *testing.T) {
	var mu sync.Mutex
	var outcomes []analyzer.Outcome
	record := func(c *analyzer.Config) {
		c.OnOutcome = func(o analyzer.Outcome) {
			mu.Lock()
			outcomes = append(outcomes, o)
			mu.Unlock()
		}
	}
	p := &handoffProvider{replies: []decideReply{pick("warn", "read only")}}
	ev := handoffEvaluator(t, p, analyzer.Handoff{Allowed: allFour}, func(c *analyzer.Config) {
		record(c)
		withCache(c)
	})
	ev.Evaluate(deleteStatement())
	ev.Evaluate(deleteStatement())

	failing := handoffEvaluator(t, &handoffProvider{}, analyzer.Handoff{Allowed: allFour}, record)
	failing.Evaluate(deleteStatement())

	want := []analyzer.Outcome{
		{Rule: "payments", Status: analyzer.StatusOK, RiskLevel: analyzer.RiskMedium, Action: analyzer.ActionWarn},
		{Rule: "payments", Status: analyzer.StatusCached, RiskLevel: analyzer.RiskMedium, Action: analyzer.ActionWarn},
		{Rule: "payments", Status: analyzer.StatusOK, RiskLevel: analyzer.RiskMedium, Action: analyzer.ActionBlock},
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(outcomes, want) {
		t.Errorf("outcomes = %+v, want %+v", outcomes, want)
	}
}
