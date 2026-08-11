package aianalyzer

import (
	"context"
	"testing"
)

// scriptedClient returns pre-programmed responses in sequence, capturing each
// request so tests can assert on the tool choice.
type scriptedClient struct {
	responses []*ChatResponse
	calls     int
	requests  []ChatRequest
}

func (c *scriptedClient) Chat(_ context.Context, req ChatRequest) (*ChatResponse, error) {
	c.requests = append(c.requests, req)
	if c.calls >= len(c.responses) {
		// Fallback: no tool call, forces the engine's final-classify path.
		c.calls++
		return &ChatResponse{}, nil
	}
	resp := c.responses[c.calls]
	c.calls++
	return resp, nil
}

// fakeExecutor returns canned output for any tool call.
type fakeExecutor struct {
	output  string
	isError bool
	calls   []string
}

func (e *fakeExecutor) Execute(_ context.Context, name, _ string) (string, bool) {
	e.calls = append(e.calls, name)
	return e.output, e.isError
}

func TestAnalyzeAgentic_InvestigatesThenClassifies(t *testing.T) {
	client := &scriptedClient{responses: []*ChatResponse{
		{
			Content: "Let me check the query cost.",
			ToolCalls: []ToolCall{
				{ID: "call_1", Name: "run_metadata_query", Arguments: `{"operation":"explain","query":"DELETE FROM t"}`},
			},
		},
		{
			ToolCalls: []ToolCall{
				{ID: "call_2", Name: "HighRiskAISessionAnalyzer", Arguments: `{"title":"Mass delete","explanation":"Deletes all rows","summary":"The DELETE targets the whole table; plan shows a full scan and no WHERE clause. High blast radius."}`},
			},
		},
	}}
	exec := &fakeExecutor{output: "Seq Scan on t (cost=0.00..431.00 rows=10000)"}

	res, err := AnalyzeAgentic(context.Background(), client, AgenticRequest{
		Content:  "DELETE FROM t;",
		Tools:    []Tool{{Name: "run_metadata_query"}, {Name: "search_past_sessions"}},
		Executor: exec,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RiskLevel != RiskLevelHigh {
		t.Errorf("RiskLevel = %q, want high", res.RiskLevel)
	}
	if res.Summary == "" {
		t.Error("Summary is empty, want populated reviewer summary")
	}
	if len(exec.calls) != 1 || exec.calls[0] != "run_metadata_query" {
		t.Errorf("executor calls = %v, want one run_metadata_query", exec.calls)
	}

	// Expect: thinking, tool_call, tool_result, then terminal tool_call.
	var toolCall, toolResult, terminal bool
	for i, s := range res.Steps {
		switch {
		case s.Type == "tool_call" && s.ToolName == "run_metadata_query":
			toolCall = true
		case s.Type == "tool_result" && s.ToolName == "run_metadata_query":
			if !toolCall {
				t.Errorf("tool_result at %d appeared before its tool_call", i)
			}
			toolResult = true
		case s.Type == "tool_call" && s.ToolName == "HighRiskAISessionAnalyzer":
			if !toolResult {
				t.Errorf("terminal classification at %d appeared before investigation completed", i)
			}
			terminal = true
		}
	}
	if !toolCall || !toolResult || !terminal {
		t.Errorf("trace missing steps: tool_call=%v tool_result=%v terminal=%v (steps=%+v)", toolCall, toolResult, terminal, res.Steps)
	}
}

func TestAnalyzeAgentic_ForcedFinalClassification(t *testing.T) {
	// The model never calls a risk tool across the iteration budget; the engine
	// must issue a final forced-classify turn (ToolChoice=any) that yields a verdict.
	responses := make([]*ChatResponse, 0, defaultMaxIterations+1)
	for range defaultMaxIterations {
		responses = append(responses, &ChatResponse{
			ToolCalls: []ToolCall{
				{ID: "call_x", Name: "search_past_sessions", Arguments: `{"scope":"current_connection"}`},
			},
		})
	}
	// Final forced turn returns a verdict.
	responses = append(responses, &ChatResponse{
		ToolCalls: []ToolCall{
			{ID: "final", Name: "LowRiskAISessionAnalyzer", Arguments: `{"title":"Routine","explanation":"read only"}`},
		},
	})
	client := &scriptedClient{responses: responses}
	exec := &fakeExecutor{output: "[]"}

	res, err := AnalyzeAgentic(context.Background(), client, AgenticRequest{
		Content:  "SELECT 1;",
		Tools:    []Tool{{Name: "search_past_sessions"}},
		Executor: exec,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RiskLevel != RiskLevelLow {
		t.Errorf("RiskLevel = %q, want low", res.RiskLevel)
	}
	// The final forced request must constrain the model to the risk tools only.
	last := client.requests[len(client.requests)-1]
	if last.ToolChoice != ToolChoiceAny {
		t.Errorf("final request ToolChoice = %q, want %q", last.ToolChoice, ToolChoiceAny)
	}
	if len(last.Tools) != len(sessionAnalyzerTools) {
		t.Errorf("final request offered %d tools, want only the %d risk tools", len(last.Tools), len(sessionAnalyzerTools))
	}
}

// The cross-provider contract both SDKs reject when violated: the assistant turn
// carrying ToolCalls must precede the RoleTool results, and every tool result
// must reference its call's ID.
func TestAnalyzeAgentic_BuildsPairedToolHistory(t *testing.T) {
	client := &scriptedClient{responses: []*ChatResponse{
		{
			Content: "checking",
			ToolCalls: []ToolCall{
				{ID: "call_a", Name: "search_past_sessions", Arguments: `{"scope":"same_type"}`},
				{ID: "call_b", Name: "run_metadata_query", Arguments: `{"operation":"table_size","table":"t"}`},
			},
		},
		{ToolCalls: []ToolCall{{ID: "t", Name: "LowRiskAISessionAnalyzer", Arguments: `{"title":"ok","explanation":"fine"}`}}},
	}}

	if _, err := AnalyzeAgentic(context.Background(), client, AgenticRequest{
		Content:  "SELECT 1;",
		Executor: &fakeExecutor{output: "{}"},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// History sent on the second turn: user, assistant(2 tool calls), tool, tool.
	msgs := client.requests[1].Messages
	if len(msgs) != 4 {
		t.Fatalf("history has %d messages, want 4: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != RoleUser {
		t.Errorf("msgs[0].Role = %q, want user", msgs[0].Role)
	}
	if msgs[1].Role != RoleAssistant || len(msgs[1].ToolCalls) != 2 {
		t.Fatalf("msgs[1] must be the assistant turn carrying both tool calls: %+v", msgs[1])
	}
	for i, wantID := range []string{"call_a", "call_b"} {
		m := msgs[2+i]
		if m.Role != RoleTool {
			t.Errorf("msgs[%d].Role = %q, want tool", 2+i, m.Role)
		}
		if m.ToolResult == nil || m.ToolResult.ToolCallID != wantID {
			t.Errorf("msgs[%d] tool result = %+v, want ToolCallID %q", 2+i, m.ToolResult, wantID)
		}
	}
}

// When the caller's deadline expires mid-investigation the loop must still
// deliver a verdict: the forced-classification turn runs on a detached budget,
// so it must receive a live context rather than the dead one.
func TestAnalyzeAgentic_DeadlineStillYieldsVerdict(t *testing.T) {
	client := &ctxRecordingClient{resp: &ChatResponse{
		ToolCalls: []ToolCall{{ID: "t", Name: "MediumRiskAISessionAnalyzer", Arguments: `{"title":"partial","explanation":"from partial evidence"}`}},
	}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // caller deadline already blown

	res, err := AnalyzeAgentic(ctx, client, AgenticRequest{Content: "DELETE FROM t;", Executor: &fakeExecutor{}})
	if err != nil {
		t.Fatalf("expired context must still produce a fallback verdict, got: %v", err)
	}
	if res.RiskLevel != RiskLevelMedium {
		t.Errorf("RiskLevel = %q, want medium", res.RiskLevel)
	}
	if client.calls != 1 {
		t.Fatalf("expected only the forced-classification call, got %d", client.calls)
	}
	if client.lastCtxErr != nil {
		t.Errorf("forced classification received a dead context (%v); it must run on a detached budget", client.lastCtxErr)
	}
}

// ctxRecordingClient captures the liveness of the context each call receives.
type ctxRecordingClient struct {
	resp       *ChatResponse
	calls      int
	lastCtxErr error
}

func (c *ctxRecordingClient) Chat(ctx context.Context, _ ChatRequest) (*ChatResponse, error) {
	c.calls++
	c.lastCtxErr = ctx.Err()
	if c.lastCtxErr != nil {
		return nil, c.lastCtxErr
	}
	return c.resp, nil
}

// The risk level is carried by the tool NAME, so a truncated argument payload
// must still yield a verdict rather than discard the whole investigation.
func TestAnalyzeAgentic_MalformedTerminalArgsStillClassify(t *testing.T) {
	client := &scriptedClient{responses: []*ChatResponse{
		{ToolCalls: []ToolCall{{ID: "t", Name: "HighRiskAISessionAnalyzer", Arguments: `{"title":"trunc`}}},
	}}
	res, err := AnalyzeAgentic(context.Background(), client, AgenticRequest{Content: "DROP TABLE t;"})
	if err != nil {
		t.Fatalf("malformed arguments must degrade, not fail: %v", err)
	}
	if res.RiskLevel != RiskLevelHigh {
		t.Errorf("RiskLevel = %q, want high (recovered from the tool name)", res.RiskLevel)
	}
	if res.Title == "" || res.Explanation == "" {
		t.Errorf("degraded verdict must carry placeholder text, got %+v", res.Result)
	}
}

// A text-only turn must be preserved in history AND followed by a user turn:
// Anthropic rejects a trailing assistant message when tool choice is forced.
func TestAnalyzeAgentic_TextOnlyTurnEndsHistoryOnUser(t *testing.T) {
	client := &scriptedClient{responses: []*ChatResponse{
		{Content: "I believe this is a routine read."},
		{ToolCalls: []ToolCall{{ID: "t", Name: "LowRiskAISessionAnalyzer", Arguments: `{"title":"ok","explanation":"read only"}`}}},
	}}
	if _, err := AnalyzeAgentic(context.Background(), client, AgenticRequest{Content: "SELECT 1;"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	msgs := client.requests[len(client.requests)-1].Messages
	if len(msgs) < 3 {
		t.Fatalf("forced-turn history too short: %+v", msgs)
	}
	assistant := msgs[len(msgs)-2]
	if assistant.Role != RoleAssistant || assistant.Content != "I believe this is a routine read." {
		t.Errorf("model's closing analysis missing from forced-turn history: %+v", assistant)
	}
	if last := msgs[len(msgs)-1]; last.Role != RoleUser {
		t.Errorf("forced-turn history ends on %q; must end on a user turn", last.Role)
	}
}

// Anthropic rejects any request whose conversation ends with an assistant
// message ("This model does not support assistant message prefill. The
// conversation must end with a user message." — verified against the live API),
// so EVERY path that reaches the forced-classification turn must leave the
// history ending on a user-role turn. Tool results serialize as a user turn.
func TestAnalyzeAgentic_ForcedTurnNeverEndsOnAssistant(t *testing.T) {
	verdict := &ChatResponse{ToolCalls: []ToolCall{
		{ID: "t", Name: "LowRiskAISessionAnalyzer", Arguments: `{"title":"ok","explanation":"fine"}`},
	}}
	investigate := func() *ChatResponse {
		return &ChatResponse{
			Content:   "let me look",
			ToolCalls: []ToolCall{{ID: "c", Name: "search_past_sessions", Arguments: `{"scope":"current_connection"}`}},
		}
	}

	cases := map[string][]*ChatResponse{
		// Model answers in prose instead of calling a tool.
		"text only": {{Content: "I think it is fine."}, verdict},
		// Model returns neither text nor tool calls.
		"empty response": {{}, verdict},
		// Investigation, then prose.
		"investigate then text": {investigate(), {Content: "done looking"}, verdict},
	}
	// Iteration budget exhausted without a verdict.
	exhausted := make([]*ChatResponse, 0, defaultMaxIterations+1)
	for range defaultMaxIterations {
		exhausted = append(exhausted, investigate())
	}
	cases["budget exhausted"] = append(exhausted, verdict)

	for name, responses := range cases {
		t.Run(name, func(t *testing.T) {
			client := &scriptedClient{responses: responses}
			if _, err := AnalyzeAgentic(context.Background(), client, AgenticRequest{
				Content:  "SELECT 1;",
				Tools:    []Tool{{Name: "search_past_sessions"}},
				Executor: &fakeExecutor{output: "[]"},
			}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			forced := client.requests[len(client.requests)-1]
			if forced.ToolChoice != ToolChoiceAny {
				t.Fatalf("last request is not the forced turn: ToolChoice=%q", forced.ToolChoice)
			}
			last := forced.Messages[len(forced.Messages)-1]
			if last.Role == RoleAssistant {
				t.Errorf("forced-turn history ends on an assistant turn; Anthropic rejects this with 400")
			}
			if last.Role != RoleUser && last.Role != RoleTool {
				t.Errorf("forced-turn history ends on %q, want user or tool", last.Role)
			}
		})
	}
}
