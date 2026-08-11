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
