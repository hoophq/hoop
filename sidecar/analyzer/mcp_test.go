package analyzer_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

func mcpCall(tool, args string) inspect.Statement {
	return inspect.Statement{
		Protocol:  inspect.MCP,
		Direction: inspect.FromClient,
		Text:      tool + "\n" + args,
		Operation: inspect.OpCall,
		Tables:    []string{strings.ToLower(tool)},
		Metadata:  map[string]string{inspect.MetadataMCPMethod: "tools/call", inspect.MetadataMCPTool: tool},
	}
}

func mcpSQL(tool, sql string, op inspect.Operation, tables ...string) inspect.Statement {
	return inspect.Statement{
		Protocol:  inspect.MCP,
		Direction: inspect.FromClient,
		Text:      sql,
		Operation: op,
		Tables:    tables,
		Metadata:  map[string]string{inspect.MetadataMCPMethod: "tools/call", inspect.MetadataMCPTool: tool, inspect.MetadataMCPSQLIndex: "1"},
	}
}

func TestMCPBuilderSendsTheToolNameAndArguments(t *testing.T) {
	b, ok := analyzer.BuilderFor(inspect.MCP)
	if !ok {
		t.Fatal("no content builder for mcp")
	}
	c, ok := b.Build(mcpCall("delete_customer", `{"id":"7"}`), analyzer.DefaultMaxInputBytes)
	if !ok || c.Text != "Protocol: mcp\nTool: delete_customer\n\nArguments:\n{\"id\":\"7\"}" {
		t.Fatalf("content %q, %v", c.Text, ok)
	}
	spaced, _ := b.Build(mcpCall("delete_customer", `{"id": "7"}`), analyzer.DefaultMaxInputBytes)
	if again, _ := b.Build(mcpCall("delete_customer", "{\"id\":\n  \"7\"}"), analyzer.DefaultMaxInputBytes); again.CacheKey != spaced.CacheKey {
		t.Error("a whitespace run changed the cache key")
	}
	for _, other := range []inspect.Statement{mcpCall("delete_customer", `{"id":"8"}`), mcpCall("get_customer", `{"id":"7"}`)} {
		if o, _ := b.Build(other, analyzer.DefaultMaxInputBytes); o.CacheKey == c.CacheKey {
			t.Errorf("%q shares a cache key with another call", other.Text)
		}
	}

	big, ok := b.Build(mcpCall("run", `{"q":"`+strings.Repeat("x", 20<<10)+`"}`), analyzer.DefaultMaxInputBytes)
	if !ok || len(big.Text) > analyzer.DefaultMaxInputBytes+len("\n...[truncated]") || !strings.HasSuffix(big.Text, "[truncated]") {
		t.Fatalf("20 KiB of arguments sent %d bytes, want at most 8 KiB and a marker", len(big.Text))
	}
	if long, _ := b.Build(mcpCall(strings.Repeat("t", 20<<10), `{}`), analyzer.DefaultMaxInputBytes); len(long.Text) > analyzer.DefaultMaxInputBytes+len("\n...[truncated]") {
		t.Fatalf("a long tool name sent %d bytes", len(long.Text))
	}

	sql, ok := b.Build(mcpSQL("execute_sql", "UPDATE t SET a = 1", inspect.OpUpdate, "t"), analyzer.DefaultMaxInputBytes)
	if !ok || !strings.Contains(sql.Text, "Operation: update") || !strings.Contains(sql.Text, "UPDATE t SET a = 1") || strings.Contains(sql.Text, "Tool:") {
		t.Fatalf("SQL from a tool call: %q, %v", sql.Text, ok)
	}

	carrier := mcpCall("execute_sql", `{"query":"SELECT 1","note":"DROP TABLE customers"}`)
	carrier.Metadata[inspect.MetadataMCPSQLStatements] = "1"
	if c, ok := b.Build(carrier, analyzer.DefaultMaxInputBytes); !ok || !strings.Contains(c.Text, "Tool: execute_sql") || !strings.Contains(c.Text, `"note":"DROP TABLE customers"`) {
		t.Fatalf("the call of a SQL tool: %q, %v", c.Text, ok)
	}
	list := inspect.Statement{Protocol: inspect.MCP, Direction: inspect.FromClient, Operation: inspect.OpShow,
		Text: "echo: Echo\n", Metadata: map[string]string{inspect.MetadataMCPMethod: "tools/list"}}
	if c, ok := b.Build(list, analyzer.DefaultMaxInputBytes); ok {
		t.Errorf("a catalog was sent: %q", c.Text)
	}
}

// A call to a SQL tool is one model call for the call and one per SQL
// statement. A trigger on call or on the tool name selects both.
func TestAnMCPSQLToolCallIsOneModelCallPlusOnePerStatement(t *testing.T) {
	call := mcpCall("execute_sql", `{"query":"UPDATE t SET a = 1","note":"DROP TABLE customers"}`)
	call.Metadata[inspect.MetadataMCPSQLStatements] = "1"
	stmts := []inspect.Statement{call, mcpSQL("execute_sql", "UPDATE t SET a = 1", inspect.OpUpdate, "t")}
	ops := func(o ...inspect.Operation) []inspect.Operation { return o }
	for _, tc := range []struct {
		name      string
		trigger   analyzer.Trigger
		call, sql bool
	}{
		{"every statement", analyzer.Trigger{All: true}, true, true},
		{"call", analyzer.Trigger{Operations: ops(inspect.OpCall)}, true, true},
		{"the SQL operation", analyzer.Trigger{Operations: ops(inspect.OpUpdate)}, false, true},
		{"the tool name", analyzer.Trigger{Tables: []string{"EXECUTE_SQL"}}, true, true},
		{"the SQL table", analyzer.Trigger{Tables: []string{"t"}}, false, true},
		{"another operation", analyzer.Trigger{Operations: ops(inspect.OpDelete)}, false, false},
		{"another tool", analyzer.Trigger{Tables: []string{"list_tables"}}, false, false},
		{"excluding the tool", analyzer.Trigger{All: true, Exclude: []analyzer.TriggerItem{{Tables: []string{"execute_sql"}}}}, false, false},
		{"excluding call keeps the SQL", analyzer.Trigger{All: true, Exclude: []analyzer.TriggerItem{{Operations: ops(inspect.OpCall)}}}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &recordingProvider{}
			ev := mustNew(t, analyzer.Config{Provider: p, Trigger: tc.trigger})
			for _, st := range stmts {
				ev.Evaluate(st)
			}
			var want []string
			if tc.call {
				want = append(want, `"note":"DROP TABLE customers"`)
			}
			if tc.sql {
				want = append(want, "Operation: update")
			}
			if len(p.prompts) != len(want) {
				t.Fatalf("the model was called %d times, want %d: %q", len(p.prompts), len(want), p.prompts)
			}
			for i, w := range want {
				if !strings.Contains(p.prompts[i], w) {
					t.Errorf("model call %d does not carry %q: %q", i, w, p.prompts[i])
				}
			}
		})
	}
}

// A hold files the tool name and the exact arguments, waits, and forwards the
// call once a claim spends the approval.
func TestAHoldOnAnMCPToolCallWaitsForTheApproval(t *testing.T) {
	rev := &recordingReviewer{
		res:    analyzer.ReviewResult{ID: "r1", Status: "PENDING"},
		claims: []analyzer.ReviewResult{{ID: "r1", Status: "PENDING"}, {Forward: true, ID: "r1", Status: "EXECUTED"}},
	}
	ev := holdingEvaluator(t, rev, func(c *analyzer.Config) { c.Trigger = analyzer.Trigger{All: true} })
	v := ev.Evaluate(mcpCall("delete_customer", `{"id": "7"}`))
	if v.Denied || v.Annotations[analyzer.MetadataReviewMode] != "hold" {
		t.Fatalf("verdict %+v", v)
	}
	if sent := rev.statements(); len(sent) != 1 || sent[0] != "delete_customer\n{\"id\": \"7\"}" {
		t.Fatalf("filed %q", sent)
	}
	if claimed := rev.claimedIDs(); len(claimed) != 2 {
		t.Fatalf("claimed %q, want two claims", claimed)
	}
}

// In return mode the call is refused at once with the approval id.
func TestAnMCPToolCallInReturnModeAnswersWithTheApprovalID(t *testing.T) {
	rev := &recordingReviewer{res: analyzer.ReviewResult{ID: "r1", Status: "PENDING"}}
	ev := holdingEvaluator(t, rev, func(c *analyzer.Config) {
		c.Trigger = analyzer.Trigger{All: true}
		c.ReviewMode = analyzer.ReviewReturn
	})
	v := ev.Evaluate(mcpCall("delete_customer", `{"id":"7"}`))
	if !v.Denied || v.Review == nil || v.Review.ID != "r1" || !v.Review.Return || !strings.Contains(v.Message, "approval r1") {
		t.Fatalf("verdict %+v", v)
	}
	if claimed := rev.claimedIDs(); len(claimed) != 0 {
		t.Fatalf("return mode waited: claimed %q", claimed)
	}
}

// WithHoldWait ends the wait sooner, even before the first poll, and the
// refusal names the approval. A limit past the hold's own wait changes nothing.
func TestWithHoldWaitEndsTheWaitWithTheApprovalID(t *testing.T) {
	for _, c := range []struct {
		name        string
		wait, limit time.Duration
		want        string
	}{
		{"shorter limit", time.Minute, 20 * time.Millisecond, "still waiting for approval after 20ms"},
		{"longer limit", 30 * time.Millisecond, time.Minute, "still waiting for approval after 30ms"},
	} {
		t.Run(c.name, func(t *testing.T) {
			rev := &recordingReviewer{res: analyzer.ReviewResult{ID: "r1", Status: "PENDING"}}
			ev := holdingEvaluator(t, rev, func(c *analyzer.Config) { c.Trigger = analyzer.Trigger{All: true} })
			analyzer.SetReviewPacing(ev, c.wait, time.Hour)
			ec := &policy.EvalContext{ConnCtx: analyzer.WithHoldWait(context.Background(), c.limit)}
			v := ev.EvaluateWith(mcpCall("delete_customer", `{"id":"7"}`), ec)
			if !v.Denied || v.Review == nil || v.Review.Status != "PENDING" ||
				!strings.Contains(v.Message, c.want) || !strings.Contains(v.Message, "(approval r1)") {
				t.Fatalf("verdict %+v", v)
			}
			if claimed := rev.claimedIDs(); len(claimed) != 1 {
				t.Fatalf("claimed %q, want one claim at the limit", claimed)
			}
		})
	}
}

// The approval agent decides a held tool call as it decides any statement.
func TestTheApprovalAgentDecidesAHeldMCPToolCall(t *testing.T) {
	p := &agentProvider{calls: []analyzer.ToolCall{call("approve", `{"reason_code":"in_scope","reason":"ok"}`)}}
	ev := agentEvaluator(t, p, func(c *analyzer.Config) { c.Trigger = analyzer.Trigger{All: true} })
	v := ev.Evaluate(mcpCall("delete_customer", `{"id":"7"}`))
	if v.Denied || v.Annotations[analyzer.MetadataApprovalDecision] != "approved" {
		t.Fatalf("verdict %+v", v)
	}
}
