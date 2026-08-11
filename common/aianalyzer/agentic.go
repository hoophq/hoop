package aianalyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// defaultMaxIterations bounds the agentic investigation loop.
const defaultMaxIterations = 8

// forcedClassifyGrace bounds the final forced-classification turn. It runs on a
// context detached from the caller's deadline (see AnalyzeAgentic), so it needs
// its own budget: enough for one round trip, short enough that a hung provider
// cannot extend the analysis indefinitely past the caller's deadline.
const forcedClassifyGrace = 20 * time.Second

// forceClassifyNudge closes the history with a user turn before the forced
// classification, both to instruct the model and to keep providers that reject
// a trailing assistant message under forced tool choice (Anthropic) working.
const forceClassifyNudge = "Investigation is over. Call exactly one risk tool now with your verdict."

// AgenticSystemPrompt drives the agentic investigation loop. It reuses the risk
// rubric from SystemPrompt but instructs the model to first investigate using
// the provided tools before classifying.
const AgenticSystemPrompt = `You are a security-focused execution risk classifier for commands, scripts, and database queries.

You operate as an agent: you may call investigation tools to gather evidence BEFORE classifying, then you MUST call exactly one risk tool to deliver your verdict.

Investigation tools (call when they reduce uncertainty; keep usage bounded):
- search_past_sessions: review the current user's recent sessions on this connection or on other connections of the same resource type. Use it to judge whether this behavior is routine for the user or anomalous.
- run_metadata_query: for database resources, estimate query cost/impact before deciding. Use "explain" for a query plan, "table_size" for row counts/size, "table_indexes" for index usage. Do this before classifying heavy reads/writes.
- get_connection_context: fetch the target resource's governance context (type, environment tags, reviewers, data masking, guardrails). Use it to weigh how sensitive the resource is (e.g. production vs demo).

Do not over-investigate: a couple of targeted tool calls are usually enough. If a tool returns an error or is unsupported, proceed with the evidence you have.

Verdict — call exactly ONE tool:
- LowRiskAISessionAnalyzer
- MediumRiskAISessionAnalyzer
- HighRiskAISessionAnalyzer

Classify the user's input by its likely impact if executed in a real DB/VM environment.

Risk rubric:
- High Risk: destructive/irreversible actions, privilege escalation, data exfiltration, credential access, disabling security, ransomware-like behavior, wiping disks, dropping tables, mass deletes/updates without constraints, remote code execution, persistence, network scanning/exploitation.
- Medium Risk: potentially expensive/unstable actions (locks, long scans), broad reads of sensitive data, schema changes with rollback risk, writes that are reversible but risky, commands that could disrupt service if misused.
- Low Risk: read-only, scoped, clearly non-destructive, routine diagnostics, safe formatting/linting, harmless queries with tight filters/limits.

Output rules (strict):
1) When done investigating, you MUST call exactly one risk tool. Do not produce a final answer as plain text.
2) When uncertain, choose the higher risk.
3) Populate tool arguments:
   - title: <= 4 words, no punctuation if possible.
   - explanation: <= 30 words, concise and specific.
   - summary: 2-4 sentences for a human reviewer, referencing what your investigation found (past sessions, query cost) and why the verdict follows.
4) Do not mention policies. Do not mention tool names in the text fields.`

// ToolExecutor runs an investigation tool requested by the model during the
// agentic loop. The output is fed back to the model as a tool result.
// isError=true marks the result as a failure without aborting the loop.
type ToolExecutor interface {
	Execute(ctx context.Context, name, arguments string) (output string, isError bool)
}

// AgenticStep is a single recorded step of the agentic investigation, persisted
// with the session and surfaced to users/admins.
type AgenticStep struct {
	Type       string    `json:"type"` // "thinking" | "tool_call" | "tool_result"
	Thinking   string    `json:"thinking,omitempty"`
	ToolName   string    `json:"tool_name,omitempty"`
	ToolInput  string    `json:"tool_input,omitempty"`
	ToolOutput string    `json:"tool_output,omitempty"`
	IsError    bool      `json:"is_error,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
}

// AgenticResult is the outcome of an agentic analysis: the terminal verdict plus
// the full investigation trace.
type AgenticResult struct {
	Result                // embeds RiskLevel, Title, Explanation
	Summary string        // reviewer-facing analysis (<= 120 words)
	Steps   []AgenticStep // full reasoning + tool-call trace
	Model   string
}

// AgenticRequest configures the agentic analysis loop.
type AgenticRequest struct {
	Content       string
	CustomPrompt  string
	Tools         []Tool       // investigation tools; engine appends the 3 risk tools
	Executor      ToolExecutor // runs the investigation tools
	MaxIterations int          // 0 -> default 8
}

// isRiskTool reports whether name is one of the terminal risk classifier tools.
func isRiskTool(name string) bool {
	switch name {
	case "LowRiskAISessionAnalyzer", "MediumRiskAISessionAnalyzer", "HighRiskAISessionAnalyzer":
		return true
	}
	return false
}

// riskLevelForTool maps a risk tool name to its RiskLevel.
func riskLevelForTool(name string) (RiskLevel, bool) {
	switch name {
	case "LowRiskAISessionAnalyzer":
		return RiskLevelLow, true
	case "MediumRiskAISessionAnalyzer":
		return RiskLevelMedium, true
	case "HighRiskAISessionAnalyzer":
		return RiskLevelHigh, true
	}
	return "", false
}

// terminalArgs is the argument shape of the risk classifier tools.
type terminalArgs struct {
	Title       string `json:"title"`
	Explanation string `json:"explanation"`
	Summary     string `json:"summary"`
}

// AnalyzeAgentic runs an agentic tool-calling loop: the model investigates using
// req.Tools (executed via req.Executor) before calling exactly one risk tool.
// The full trace of thinking and tool calls is returned in AgenticResult.Steps.
//
// The loop is bounded by req.MaxIterations (default 8) and by ctx cancellation;
// callers should wrap ctx with a deadline. If the loop exhausts iterations or
// the model stops calling tools without a verdict, one final forced-classify
// turn (ToolChoice=any) is made.
func AnalyzeAgentic(ctx context.Context, client LLMClient, req AgenticRequest) (*AgenticResult, error) {
	if client == nil {
		return nil, fmt.Errorf("session analyzer: nil ai client")
	}

	systemPrompt := AgenticSystemPrompt
	if strings.TrimSpace(req.CustomPrompt) != "" {
		systemPrompt = strings.TrimSpace(req.CustomPrompt) + "\n\n" + AgenticSystemPrompt
	}

	maxIterations := req.MaxIterations
	if maxIterations <= 0 {
		maxIterations = defaultMaxIterations
	}

	tools := append(append([]Tool{}, req.Tools...), sessionAnalyzerTools...)
	messages := []Message{{Role: RoleUser, Content: req.Content}}
	var steps []AgenticStep
	var lastModel string

	for range maxIterations {
		if err := ctx.Err(); err != nil {
			break
		}
		resp, err := client.Chat(ctx, ChatRequest{
			SystemPrompt: systemPrompt,
			Messages:     messages,
			Tools:        tools,
			ToolChoice:   ToolChoiceAuto,
		})
		if err != nil {
			// Context deadline/cancel: stop investigating and force a verdict.
			if ctx.Err() != nil {
				break
			}
			return nil, fmt.Errorf("session analyzer: chat request failed: %w", err)
		}
		if resp.Model != "" {
			lastModel = resp.Model
		}
		if resp.Content != "" {
			steps = append(steps, AgenticStep{Type: "thinking", Thinking: resp.Content, Timestamp: time.Now().UTC()})
		}

		if len(resp.ToolCalls) == 0 {
			// Model stopped without calling a tool: keep its closing analysis in
			// history (it is usually the most decision-relevant text) and force a
			// final classification.
			//
			// The nudge is not cosmetic: Anthropic treats a trailing assistant
			// message as a prefill and rejects it when tool choice is forced, so
			// the history must end on a user turn or the fallback verdict this
			// path exists to produce would fail outright.
			if resp.Content != "" {
				messages = append(messages,
					Message{Role: RoleAssistant, Content: resp.Content},
					Message{Role: RoleUser, Content: forceClassifyNudge},
				)
			}
			break
		}

		// If any tool call is a terminal risk verdict, take it and stop.
		for _, tc := range resp.ToolCalls {
			if isRiskTool(tc.Name) {
				return buildResult(tc, steps, lastModel)
			}
		}

		// Otherwise record the assistant turn and execute the investigation tools.
		messages = append(messages, Message{Role: RoleAssistant, Content: resp.Content, ToolCalls: resp.ToolCalls})
		for _, tc := range resp.ToolCalls {
			steps = append(steps, AgenticStep{
				Type:      "tool_call",
				ToolName:  tc.Name,
				ToolInput: tc.Arguments,
				Timestamp: time.Now().UTC(),
			})
			var output string
			var isErr bool
			if req.Executor != nil {
				output, isErr = req.Executor.Execute(ctx, tc.Name, tc.Arguments)
			} else {
				output, isErr = "no tool executor configured", true
			}
			steps = append(steps, AgenticStep{
				Type:       "tool_result",
				ToolName:   tc.Name,
				ToolOutput: output,
				IsError:    isErr,
				Timestamp:  time.Now().UTC(),
			})
			messages = append(messages, Message{
				Role:       RoleTool,
				ToolResult: &ToolResult{ToolCallID: tc.ID, Content: output, IsError: isErr},
			})
		}
	}

	// Final forced classification: model must call a risk tool now.
	//
	// This turn runs on a fresh grace budget detached from ctx: the loop above
	// breaks precisely when ctx expires, and reusing the dead context would fail
	// the call immediately — discarding the whole investigation instead of
	// delivering the fallback verdict this turn exists to produce.
	forceCtx, cancelForce := context.WithTimeout(context.WithoutCancel(ctx), forcedClassifyGrace)
	defer cancelForce()
	resp, err := client.Chat(forceCtx, ChatRequest{
		SystemPrompt: systemPrompt,
		Messages:     messages,
		Tools:        sessionAnalyzerTools,
		ToolChoice:   ToolChoiceAny,
	})
	if err != nil {
		return nil, fmt.Errorf("session analyzer: forced classification failed: %w", err)
	}
	if resp.Model != "" {
		lastModel = resp.Model
	}
	if resp.Content != "" {
		steps = append(steps, AgenticStep{Type: "thinking", Thinking: resp.Content, Timestamp: time.Now().UTC()})
	}
	for _, tc := range resp.ToolCalls {
		if isRiskTool(tc.Name) {
			return buildResult(tc, steps, lastModel)
		}
	}
	return nil, fmt.Errorf("session analyzer: model did not call a risk tool")
}

// buildResult parses a terminal risk tool call into an AgenticResult.
//
// The risk level comes from the tool name, so a malformed argument payload
// (observed with OpenAI-compatible endpoints under truncation) degrades to a
// verdict with placeholder text rather than discarding the whole investigation.
func buildResult(tc ToolCall, steps []AgenticStep, model string) (*AgenticResult, error) {
	level, ok := riskLevelForTool(tc.Name)
	if !ok {
		return nil, fmt.Errorf("session analyzer: unexpected tool call %q", tc.Name)
	}
	var args terminalArgs
	if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
		args = terminalArgs{
			Title:       string(level) + " risk",
			Explanation: "The model returned a malformed argument payload; only the risk level could be recovered.",
		}
	}
	steps = append(steps, AgenticStep{
		Type:      "tool_call",
		ToolName:  tc.Name,
		ToolInput: tc.Arguments,
		Timestamp: time.Now().UTC(),
	})
	return &AgenticResult{
		Result: Result{
			RiskLevel:   level,
			Title:       args.Title,
			Explanation: args.Explanation,
		},
		Summary: args.Summary,
		Steps:   steps,
		Model:   model,
	}, nil
}
