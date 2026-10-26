package analyzer_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
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

// handoffEvaluator rates every delete medium and hands medium off.
func handoffEvaluator(t *testing.T, p *handoffProvider, h analyzer.Handoff, edit func(*analyzer.Config)) *analyzer.Evaluator {
	t.Helper()
	p.level = analyzer.RiskMedium
	cfg := analyzer.Config{
		Rule:     "payments",
		Listener: "payments",
		Provider: p,
		Trigger:  deleteTrigger(),
		Actions:  analyzer.ActionMap{analyzer.RiskMedium: analyzer.ActionHandoff},
		Handoffs: map[analyzer.RiskLevel]analyzer.Handoff{analyzer.RiskMedium: h},
		Timeout:  time.Second,
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
			for _, want := range []string{"DELETE FROM users", "Risk level: medium", "Risk title: dangerous statement",
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

// require_approval with the ai_agent approver runs the approval agent with
// the nested prompt, and files its decision as reached through the hand-off.
func TestAHandoffHoldGoesToTheApprovalAgent(t *testing.T) {
	for name, tc := range map[string]struct {
		prompt string
		want   string
	}{
		"nested prompt":  {"Approve reads only.", "Approve reads only.\n\n"},
		"default prompt": {"", analyzer.DefaultApprovalPrompt + "\n\n"},
	} {
		t.Run(name, func(t *testing.T) {
			rev := &decisionReviewer{}
			p := &handoffProvider{replies: []decideReply{pick("require_approval", "needs a check"), pick("approve", "bounded")}}
			ev := handoffEvaluator(t, p, analyzer.Handoff{Allowed: allFour,
				Approval: analyzer.Approval{Prompt: tc.prompt}}, func(c *analyzer.Config) { c.Review = rev })
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
				analyzer.MetadataHandoffDecision:  "require_approval",
				analyzer.MetadataHandoffReason:    "needs a check",
				analyzer.MetadataApprover:         "ai_agent",
				analyzer.MetadataApprovalDecision: "approved",
				analyzer.MetadataApprovalReason:   "bounded",
			} {
				if got := v.Annotations[k]; got != want {
					t.Errorf("%s = %q, want %q", k, got, want)
				}
			}
			if len(rev.got) != 1 || !rev.got[0].Handoff || rev.got[0].RiskLevel != analyzer.RiskMedium {
				t.Errorf("filed %+v, want one decision marked as a hand-off", rev.got)
			}
			if got := ev.Stats().Calls; got != 3 {
				t.Errorf("calls = %d, want classification, hand-off and approval", got)
			}
		})
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
	base := analyzer.Handoff{Prompt: "p", Allowed: allFour, Approval: analyzer.Approval{Prompt: "a"}}
	for name, changed := range map[string]analyzer.Handoff{
		"same":     base,
		"prompt":   {Prompt: "q", Allowed: allFour, Approval: analyzer.Approval{Prompt: "a"}},
		"allowed":  {Prompt: "p", Allowed: allFour[:3]},
		"approver": {Prompt: "p", Allowed: allFour, Approval: analyzer.Approval{Approver: analyzer.ApproverHuman}},
		"approval prompt": {Prompt: "p", Allowed: allFour,
			Approval: analyzer.Approval{Prompt: "b"}},
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
		"no configuration": {Provider: &handoffProvider{}, Actions: handoff},
		"configuration on another action": {Provider: &handoffProvider{},
			Actions:  analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionBlock},
			Handoffs: map[analyzer.RiskLevel]analyzer.Handoff{analyzer.RiskHigh: {Allowed: allFour}}},
		"provider without tool calls": {Provider: &stubProvider{}, Actions: handoff,
			Handoffs: map[analyzer.RiskLevel]analyzer.Handoff{analyzer.RiskHigh: {Allowed: allFour}}},
		"no allowed action": {Provider: &handoffProvider{}, Actions: handoff,
			Handoffs: map[analyzer.RiskLevel]analyzer.Handoff{analyzer.RiskHigh: {}}},
		"hand-off allowed": {Provider: &handoffProvider{}, Actions: handoff,
			Handoffs: map[analyzer.RiskLevel]analyzer.Handoff{analyzer.RiskHigh: {Allowed: []analyzer.Action{analyzer.ActionHandoff}}}},
		"unknown approver": {Provider: &handoffProvider{}, Actions: handoff,
			Handoffs: map[analyzer.RiskLevel]analyzer.Handoff{analyzer.RiskHigh: {Allowed: allFour,
				Approval: analyzer.Approval{Approver: "robot"}}}},
		"approver without a hold": {Provider: &handoffProvider{}, Actions: handoff,
			Handoffs: map[analyzer.RiskLevel]analyzer.Handoff{analyzer.RiskHigh: {Allowed: allFour[:2],
				Approval: analyzer.Approval{Approver: analyzer.ApproverHuman}}}},
	} {
		if _, err := analyzer.New(cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
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
