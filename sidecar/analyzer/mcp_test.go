package analyzer_test

import (
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/inspect"
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

	carrier := mcpCall("execute_sql", `{"query":"UPDATE t SET a = 1"}`)
	carrier.Metadata[inspect.MetadataMCPSQLStatements] = "1"
	list := inspect.Statement{Protocol: inspect.MCP, Direction: inspect.FromClient, Operation: inspect.OpShow,
		Text: "echo: Echo\n", Metadata: map[string]string{inspect.MetadataMCPMethod: "tools/list"}}
	for name, st := range map[string]inspect.Statement{"the call that carried SQL": carrier, "a catalog": list} {
		if c, ok := b.Build(st, analyzer.DefaultMaxInputBytes); ok {
			t.Errorf("%s was sent: %q", name, c.Text)
		}
	}
}

// A call to a SQL tool is one model call per SQL statement, never one more
// for the call. A trigger on call or on the tool name selects that SQL.
func TestAnMCPSQLToolCallIsOneModelCallPerStatement(t *testing.T) {
	call := mcpCall("execute_sql", `{"query":"UPDATE t SET a = 1"}`)
	call.Metadata[inspect.MetadataMCPSQLStatements] = "1"
	stmts := []inspect.Statement{call, mcpSQL("execute_sql", "UPDATE t SET a = 1", inspect.OpUpdate, "t")}
	ops := func(o ...inspect.Operation) []inspect.Operation { return o }
	for _, tc := range []struct {
		name    string
		trigger analyzer.Trigger
		calls   int
	}{
		{"every statement", analyzer.Trigger{All: true}, 1},
		{"call", analyzer.Trigger{Operations: ops(inspect.OpCall)}, 1},
		{"the SQL operation", analyzer.Trigger{Operations: ops(inspect.OpUpdate)}, 1},
		{"the tool name", analyzer.Trigger{Tables: []string{"EXECUTE_SQL"}}, 1},
		{"the SQL table", analyzer.Trigger{Tables: []string{"t"}}, 1},
		{"another operation", analyzer.Trigger{Operations: ops(inspect.OpDelete)}, 0},
		{"another tool", analyzer.Trigger{Tables: []string{"list_tables"}}, 0},
		{"excluding the tool", analyzer.Trigger{All: true, Exclude: []analyzer.TriggerItem{{Tables: []string{"execute_sql"}}}}, 0},
		{"excluding call keeps the SQL", analyzer.Trigger{All: true, Exclude: []analyzer.TriggerItem{{Operations: ops(inspect.OpCall)}}}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &recordingProvider{}
			ev := mustNew(t, analyzer.Config{Provider: p, Trigger: tc.trigger})
			for _, st := range stmts {
				ev.Evaluate(st)
			}
			if len(p.prompts) != tc.calls {
				t.Fatalf("the model was called %d times, want %d: %q", len(p.prompts), tc.calls, p.prompts)
			}
			if tc.calls == 1 && !strings.Contains(p.prompts[0], "UPDATE t SET a = 1") {
				t.Errorf("the call does not carry the SQL: %q", p.prompts[0])
			}
		})
	}
}

// ENG-967 is not done: a hold on a tool call denies at once and files nothing.
func TestAHoldOnAnMCPToolCallFilesNothing(t *testing.T) {
	rev := &recordingReviewer{res: analyzer.ReviewResult{ID: "r1", Status: "PENDING"}}
	ev := holdingEvaluator(t, rev, func(c *analyzer.Config) { c.Trigger = analyzer.Trigger{All: true} })
	v := ev.Evaluate(mcpCall("delete_customer", `{"id":"7"}`))
	if !v.Denied || !strings.Contains(v.Message, "an mcp lane does not hold a tool call for approval yet") || v.Review != nil {
		t.Fatalf("verdict %+v", v)
	}
	if sent := rev.statements(); len(sent) != 0 {
		t.Fatalf("a held tool call was filed: %q", sent)
	}
}
