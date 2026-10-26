package analyzer_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/analyzer"
	_ "github.com/hoophq/hoop/sidecar/analyzer/anthropic"
	_ "github.com/hoophq/hoop/sidecar/analyzer/gemini"
	_ "github.com/hoophq/hoop/sidecar/analyzer/openai"
	"github.com/hoophq/hoop/sidecar/policy"
)

// agentProvider classifies like stubProvider and answers the approval agent
// with fixed tool calls.
type agentProvider struct {
	stubProvider

	mu      sync.Mutex
	calls   []analyzer.ToolCall
	err     error
	delay   time.Duration
	system  string
	content string
	tools   []analyzer.ToolSpec
	decides int
}

func (a *agentProvider) Decide(ctx context.Context, system, content string, tools []analyzer.ToolSpec) ([]analyzer.ToolCall, error) {
	a.mu.Lock()
	a.system, a.content, a.tools = system, content, tools
	a.decides++
	a.mu.Unlock()
	if a.delay > 0 {
		select {
		case <-time.After(a.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return a.calls, a.err
}

func call(name string, args string) analyzer.ToolCall {
	if args == "" {
		return analyzer.ToolCall{Name: name}
	}
	return analyzer.ToolCall{Name: name, Args: json.RawMessage(args)}
}

func agentEvaluator(t *testing.T, p *agentProvider, edit func(*analyzer.Config)) *analyzer.Evaluator {
	t.Helper()
	p.level = analyzer.RiskHigh
	cfg := analyzer.Config{
		Rule:      "payments",
		Listener:  "payments",
		Provider:  p,
		Trigger:   deleteTrigger(),
		Actions:   analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionRequireReview},
		Approvals: map[analyzer.RiskLevel]analyzer.Approval{analyzer.RiskHigh: {Approver: analyzer.ApproverAIAgent}},
		Timeout:   time.Second,
	}
	if edit != nil {
		edit(&cfg)
	}
	return mustNew(t, cfg)
}

func withUser(user string) *policy.EvalContext {
	return &policy.EvalContext{Context: map[string]string{"principal": user}}
}

// One approve with a reason forwards, and the record says who decided and why.
func TestTheApprovalAgentApproves(t *testing.T) {
	p := &agentProvider{calls: []analyzer.ToolCall{call("approve", `{"reason":"bounded by id"}`)}}
	ev := agentEvaluator(t, p, nil)

	v := ev.EvaluateWith(deleteStatement(), withUser("ana@corp"))
	if v.Denied {
		t.Fatalf("an approved statement was denied: %s", v.Message)
	}
	for k, want := range map[string]string{
		analyzer.MetadataApprover:         "ai_agent",
		analyzer.MetadataApprovalDecision: "approved",
		analyzer.MetadataApprovalReason:   "bounded by id",
		analyzer.MetadataAction:           "require_review",
	} {
		if got := v.Annotations[k]; got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if !strings.HasPrefix(p.system, analyzer.DefaultApprovalPrompt) ||
		!strings.HasSuffix(p.system, "a name or a token.") {
		t.Errorf("system prompt is not the default plus the suffix:\n%s", p.system)
	}
	for _, want := range []string{"DELETE FROM users", "Risk level: high", "Risk title: dangerous statement",
		"Connection: payments", "Protocol: postgres", "User: ana@corp"} {
		if !strings.Contains(p.content, want) {
			t.Errorf("content lacks %q:\n%s", want, p.content)
		}
	}
	if len(p.tools) != 2 || p.tools[0].Name != "approve" || p.tools[1].Name != "reject" {
		t.Fatalf("tools = %+v", p.tools)
	}
	for _, tool := range p.tools {
		if len(tool.Input.Required) != 1 || tool.Input.Required[0] != "reason" ||
			tool.Input.Properties["reason"].Type != "string" {
			t.Errorf("%s input = %+v, want a required string reason", tool.Name, tool.Input)
		}
	}
}

// A reject denies, and the user reads the reason in the error.
func TestTheApprovalAgentRejectsWithItsReason(t *testing.T) {
	p := &agentProvider{calls: []analyzer.ToolCall{call("reject", `{"reason":"no WHERE clause"}`)}}
	v := agentEvaluator(t, p, nil).Evaluate(deleteStatement())
	if !v.Denied {
		t.Fatal("a rejected statement was forwarded")
	}
	if !strings.Contains(v.Message, `the approval agent rejected the statement: "no WHERE clause"`) {
		t.Errorf("message = %q, want the reason", v.Message)
	}
	if v.Annotations[analyzer.MetadataApprovalDecision] != "rejected" {
		t.Errorf("decision = %q", v.Annotations[analyzer.MetadataApprovalDecision])
	}
}

// Anything but one approve or reject with a reason rejects: the agent fails
// closed.
func TestTheApprovalAgentFailsClosed(t *testing.T) {
	for name, p := range map[string]*agentProvider{
		"no call":        {},
		"two calls":      {calls: []analyzer.ToolCall{call("approve", `{"reason":"a"}`), call("approve", `{"reason":"b"}`)}},
		"unknown tool":   {calls: []analyzer.ToolCall{call("report_low_risk", `{"reason":"a"}`)}},
		"missing reason": {calls: []analyzer.ToolCall{call("approve", `{}`)}},
		"blank reason":   {calls: []analyzer.ToolCall{call("approve", `{"reason":"  "}`)}},
		"no arguments":   {calls: []analyzer.ToolCall{call("approve", "")}},
		"provider error": {err: errors.New("503")},
		"refusal":        {err: errors.New("model refused to answer")},
		"timeout":        {delay: 5 * time.Second, calls: []analyzer.ToolCall{call("approve", `{"reason":"late"}`)}},
	} {
		t.Run(name, func(t *testing.T) {
			ev := agentEvaluator(t, p, func(c *analyzer.Config) { c.Timeout = 50 * time.Millisecond })
			v := ev.Evaluate(deleteStatement())
			if !v.Denied {
				t.Fatal("an undecided statement was forwarded")
			}
			if !strings.Contains(v.Message, "the approval agent did not decide") {
				t.Errorf("message = %q, want the fixed reason", v.Message)
			}
			if v.Annotations[analyzer.MetadataApprovalDecision] != "rejected" {
				t.Errorf("decision = %q, want rejected", v.Annotations[analyzer.MetadataApprovalDecision])
			}
		})
	}
}

// The decision costs one call from the classification's purse; a spent
// budget rejects instead of forwarding.
func TestTheApprovalAgentSpendsTheBudget(t *testing.T) {
	p := &agentProvider{calls: []analyzer.ToolCall{call("approve", `{"reason":"ok"}`)}}
	ev := agentEvaluator(t, p, nil)
	if v := ev.Evaluate(deleteStatement()); v.Denied {
		t.Fatalf("denied: %s", v.Message)
	}
	if got := ev.Stats().Calls; got != 2 {
		t.Errorf("calls = %d, want the classification and the decision", got)
	}

	p = &agentProvider{calls: []analyzer.ToolCall{call("approve", `{"reason":"ok"}`)}}
	ev = agentEvaluator(t, p, func(c *analyzer.Config) { c.MaxCalls = 1 })
	v := ev.Evaluate(deleteStatement())
	if !v.Denied || !strings.Contains(v.Message, "did not decide") {
		t.Fatalf("a spent budget did not reject: %+v", v)
	}
	if p.decides != 0 {
		t.Errorf("the agent was asked %d time(s) past the budget", p.decides)
	}
}

// The level prompt replaces the default; the suffix stays.
func TestTheLevelPromptReplacesTheDefault(t *testing.T) {
	p := &agentProvider{calls: []analyzer.ToolCall{call("approve", `{"reason":"ok"}`)}}
	ev := agentEvaluator(t, p, func(c *analyzer.Config) {
		c.Approvals[analyzer.RiskHigh] = analyzer.Approval{Approver: analyzer.ApproverAIAgent, Prompt: "Approve reads only."}
	})
	ev.Evaluate(deleteStatement())
	if !strings.HasPrefix(p.system, "Approve reads only.") || strings.Contains(p.system, "You are an approval agent") ||
		!strings.Contains(p.system, "calling exactly one tool: approve or reject") {
		t.Errorf("system prompt:\n%s", p.system)
	}
}

// decisionReviewer is a backend that keeps decisions, like the plane.
type decisionReviewer struct {
	recordingReviewer
	got []analyzer.Decision
	err error
}

func (d *decisionReviewer) FileDecision(_ context.Context, statement string, dec analyzer.Decision) (analyzer.ReviewResult, error) {
	d.got = append(d.got, dec)
	d.sent = append(d.sent, statement)
	if d.err != nil {
		return analyzer.ReviewResult{}, d.err
	}
	return analyzer.ReviewResult{ID: "a1", Status: "APPROVED"}, nil
}

// With a backend, an approval forwards only once the backend took it; a
// rejection denies whether or not it was recorded.
func TestTheApprovalAgentFilesItsDecision(t *testing.T) {
	approve := []analyzer.ToolCall{call("approve", `{"reason":"ok"}`)}
	reject := []analyzer.ToolCall{call("reject", `{"reason":"too wide"}`)}
	for name, tc := range map[string]struct {
		calls  []analyzer.ToolCall
		err    error
		denied bool
		want   string
	}{
		"approved and filed":     {calls: approve},
		"approved, filing fails": {calls: approve, err: errors.New("down"), denied: true, want: "could not be recorded"},
		"rejected and filed":     {calls: reject, denied: true, want: `"too wide" (approval a1)`},
		"rejected, filing fails": {calls: reject, err: errors.New("down"), denied: true, want: `"too wide"`},
	} {
		t.Run(name, func(t *testing.T) {
			rev := &decisionReviewer{err: tc.err}
			ev := agentEvaluator(t, &agentProvider{calls: tc.calls}, func(c *analyzer.Config) { c.Review = rev })
			v := ev.Evaluate(deleteStatement())
			if v.Denied != tc.denied || !strings.Contains(v.Message, tc.want) {
				t.Fatalf("denied=%t message=%q, want denied=%t containing %q", v.Denied, v.Message, tc.denied, tc.want)
			}
			if len(rev.got) != 1 || rev.got[0].RiskLevel != analyzer.RiskHigh || rev.sent[0] != deleteStatement().Text {
				t.Fatalf("filed %+v %q", rev.got, rev.sent)
			}
			if wantApproved := len(tc.calls) > 0 && tc.calls[0].Name == "approve"; rev.got[0].Approved != wantApproved {
				t.Errorf("filed approved=%t", rev.got[0].Approved)
			}
			if len(rev.statements()) != 1 || len(rev.claimedIDs()) != 0 {
				t.Errorf("the agent's decision filed a human review or claimed one")
			}
		})
	}
}

// A human level files under its own approver: the reviewer reads the level.
func TestAHumanLevelFilesItsLevel(t *testing.T) {
	rev := &levelReviewer{}
	ev := mustNew(t, analyzer.Config{
		Provider:  &stubProvider{level: analyzer.RiskMedium},
		Trigger:   deleteTrigger(),
		Actions:   analyzer.ActionMap{analyzer.RiskMedium: analyzer.ActionRequireReview, analyzer.RiskHigh: analyzer.ActionRequireReview},
		Approvals: map[analyzer.RiskLevel]analyzer.Approval{analyzer.RiskMedium: {Approver: analyzer.ApproverHuman}},
		Review:    rev,
	})
	analyzer.SetReviewPacing(ev, 10*time.Millisecond, 5*time.Millisecond)
	ev.Evaluate(deleteStatement())
	if !rev.ok || rev.level != analyzer.RiskMedium {
		t.Errorf("filed level = %q (%t), want medium", rev.level, rev.ok)
	}

	// The bare form files under the lane's rule, with no level.
	rev = &levelReviewer{}
	ev = holdingEvaluator(t, nil, func(c *analyzer.Config) { c.Review = rev })
	ev.Evaluate(deleteStatement())
	if rev.ok {
		t.Errorf("a bare hold filed level %q", rev.level)
	}
}

type levelReviewer struct {
	level analyzer.RiskLevel
	ok    bool
}

func (l *levelReviewer) File(ctx context.Context, _ string) (analyzer.ReviewResult, error) {
	l.level, l.ok = analyzer.FiledLevel(ctx)
	return analyzer.ReviewResult{ID: "r1", Status: "REJECTED"}, nil
}

func (l *levelReviewer) Claim(context.Context, string) (analyzer.ReviewResult, error) {
	return analyzer.ReviewResult{}, errors.New("unused")
}

// New refuses an approver it cannot honour.
func TestNewRefusesAnApproverItCannotHonour(t *testing.T) {
	for name, cfg := range map[string]analyzer.Config{
		"agent without tool calls": {
			Provider:  &stubProvider{},
			Actions:   analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionRequireReview},
			Approvals: map[analyzer.RiskLevel]analyzer.Approval{analyzer.RiskHigh: {Approver: analyzer.ApproverAIAgent}},
		},
		"level that does not hold": {
			Provider:  &agentProvider{},
			Actions:   analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionBlock},
			Approvals: map[analyzer.RiskLevel]analyzer.Approval{analyzer.RiskHigh: {Approver: analyzer.ApproverHuman}},
		},
		"unknown approver": {
			Provider:  &agentProvider{},
			Actions:   analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionRequireReview},
			Approvals: map[analyzer.RiskLevel]analyzer.Approval{analyzer.RiskHigh: {Approver: "robot"}},
		},
	} {
		if _, err := analyzer.New(cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// realDecider classifies like stubProvider and decides through a real
// provider's encoder and parser.
type realDecider struct {
	stubProvider
	decider analyzer.ToolCaller
}

func (r *realDecider) Decide(ctx context.Context, system, content string, tools []analyzer.ToolSpec) ([]analyzer.ToolCall, error) {
	return r.decider.Decide(ctx, system, content, tools)
}

// A provider reply that refuses beside a valid approve is no decision: the
// agent rejects with the fixed reason. Each provider's own refusal signal.
func TestARefusalBesideAnApproveRejects(t *testing.T) {
	approve := `{"reason":"bounded"}`
	for name, tc := range map[string]struct{ provider, reply string }{
		"anthropic refusal": {"anthropic", `{"stop_reason":"refusal","content":[{"type":"tool_use","name":"approve","input":` + approve + `}]}`},
		"openai refusal": {"openai", `{"choices":[{"message":{"refusal":"no","tool_calls":[{"function":{"name":"approve",` +
			`"arguments":"{\"reason\":\"bounded\"}"}}]}}]}`},
		"openai content filter": {"openai", `{"choices":[{"message":{"tool_calls":[{"function":{"name":"approve",` +
			`"arguments":"{\"reason\":\"bounded\"}"}}]},"finish_reason":"content_filter"}]}`},
		"gemini safety stop": {"gemini", `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"approve","args":` +
			approve + `}}]},"finishReason":"SAFETY"}]}`},
		"gemini blocked prompt": {"gemini", `{"promptFeedback":{"blockReason":"OTHER"},"candidates":[{"content":{"parts":` +
			`[{"functionCall":{"name":"approve","args":` + approve + `}}]},"finishReason":"STOP"}]}`},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, tc.reply)
			}))
			defer srv.Close()
			p, err := analyzer.NewProvider(tc.provider, analyzer.Options{
				Endpoint: srv.URL, Model: "m", Credential: analyzer.NewSecret([]byte("k")),
			})
			if err != nil {
				t.Fatal(err)
			}
			rd := &realDecider{stubProvider: stubProvider{level: analyzer.RiskHigh}, decider: p.(analyzer.ToolCaller)}
			ev := mustNew(t, analyzer.Config{
				Rule: "payments", Listener: "payments", Provider: rd, Trigger: deleteTrigger(),
				Actions:   analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionRequireReview},
				Approvals: map[analyzer.RiskLevel]analyzer.Approval{analyzer.RiskHigh: {Approver: analyzer.ApproverAIAgent}},
			})
			v := ev.Evaluate(deleteStatement())
			if !v.Denied || !strings.Contains(v.Message, "the approval agent did not decide") {
				t.Fatalf("denied=%t message=%q, want the fixed undecided reason", v.Denied, v.Message)
			}
			if v.Annotations[analyzer.MetadataApprovalDecision] != "rejected" {
				t.Errorf("decision = %q", v.Annotations[analyzer.MetadataApprovalDecision])
			}
		})
	}
}
