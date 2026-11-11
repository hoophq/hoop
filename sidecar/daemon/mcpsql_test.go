package daemon

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/lexer"
	"github.com/hoophq/hoop/sidecar/policy"
)

// mcpSQLLane is the README BigQuery lane, as JSON.
const mcpSQLLane = `{"name":"bigquery","protocol":"mcp","listen":"127.0.0.1:18902","upstream":"https://bigquery.googleapis.com/mcp","mcp_gateway":{"sql":{"execute_sql*":{"argument":"query","dialect":"googlesql"}}}}`

func openMCPSQLSession(t *testing.T, guardrails string) (MCPLaneSession, *audit.MemorySink) {
	t.Helper()
	ln := mcpTestLane(t, `{"guardrails":`+guardrails+`,"listeners":[`+mcpSQLLane+`]}`)
	sink := audit.NewMemorySink(64)
	ls, err := openMCPLaneSession(context.Background(), ln, newLiveRules(ln), sink, true, MCPCaller{Subject: "alice", MCPSessionID: "m1"}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	return ls, sink
}

// judged is the statement and violation rows of a trail.
func judged(sink *audit.MemorySink) []audit.Event {
	var out []audit.Event
	for _, r := range sink.Events() {
		if r.Kind == audit.KindStatement || r.Kind == audit.KindViolation {
			out = append(out, r)
		}
	}
	return out
}

func TestMCPSQLValidation(t *testing.T) {
	if p := mcpProblems(t, mcpSQLLane); p != "" {
		t.Fatalf("the README lane does not validate: %s", p)
	}
	const ok = `"name":"m","protocol":"mcp","listen":"127.0.0.1:1","upstream":"https://h/mcp"`
	for _, tc := range []struct{ sql, want string }{
		{`{"execute_sql":{"argument":"query","dialect":"bigquery"}}`, `mcp_gateway.sql["execute_sql"].dialect "bigquery" is not one of clickhouse, googlesql, mssql, mysql, oracle, postgres, spark`},
		{`{"execute_sql":{"argument":"query"}}`, `mcp_gateway.sql["execute_sql"].dialect "" is not one of`},
		{`{"execute_sql":{"dialect":"googlesql"}}`, `mcp_gateway.sql["execute_sql"].argument "" is not an argument name`},
		{`{"execute_sql":{"argument":" query","dialect":"googlesql"}}`, `.argument " query" is not an argument name`},
		{`{" execute_sql":{"argument":"query","dialect":"googlesql"}}`, `mcp_gateway.sql[" execute_sql"] is not a tool glob`},
		{`{"":{"argument":"query","dialect":"googlesql"}}`, `mcp_gateway.sql[""] is not a tool glob`},
		{`{"execute_sql":{"argument":"query","dialect":"googlesql","path":"x"}}`, "unknown field"},
	} {
		if p := mcpProblems(t, `{`+ok+`,"mcp_gateway":{"sql":`+tc.sql+`}}`); !strings.Contains(p, tc.want) {
			t.Errorf("%s\n  problems %q lack %q", tc.sql, p, tc.want)
		}
	}
}

// The plane serves mcp_gateway.sql only to a build that reads it.
func TestMCPSQLNeedsTheCapability(t *testing.T) {
	cfg, err := LoadConfigBytes([]byte(`{"listeners":[` + mcpSQLLane + `]}`))
	if err != nil {
		t.Fatal(err)
	}
	old := Handshake{Capabilities: []string{"protocol:mcp", "mcp_gateway", "address_url"}}
	if err := CheckServable(*cfg, old); err == nil || !strings.Contains(err.Error(), "mcp_sql") {
		t.Fatalf("a build without mcp_sql was served the block: %v", err)
	}
	if err := CheckServable(*cfg, Handshake{Capabilities: append(slices.Clone(old.Capabilities), "mcp_sql")}); err != nil {
		t.Fatalf("a build with mcp_sql was refused: %v", err)
	}
}

// inspect.ProceduresOf reads an mcp SQL statement by its dialect name.
func TestEveryMCPSQLDialectHasALexerName(t *testing.T) {
	for name, d := range httpSQLDialects {
		if got, ok := lexer.DialectNamed(name); !ok || got != d {
			t.Errorf("%s: lexer.DialectNamed = %v, %v; want %v", name, got, ok, d)
		}
	}
}

// One call to a SQL tool is 1 + N statements: the call, then each SQL
// statement of its argument under the tool's dialect. The first deny stops it.
func TestAnMCPSQLToolCallIsJudgedPerStatement(t *testing.T) {
	ls, sink := openMCPSQLSession(t, `{"rules":[{"name":"read-only","type":"operation","operations":["insert","update","delete","drop"],"message":"read only"}]}`)
	ctx := context.Background()
	if v := ls.ToolCall(ctx, "execute_sql", []byte(`{"query":"SELECT name FROM ds.customers"}`)); !v.Allowed {
		t.Fatalf("a SELECT was denied: %+v", v)
	}
	if v := ls.ToolCall(ctx, "execute_sql", []byte(`{"query":"UPDATE ds.customers SET name = 'x' WHERE true"}`)); v.Allowed || v.Rule != "read-only" {
		t.Fatalf("an UPDATE verdict: %+v", v)
	}
	if v := ls.ToolCall(ctx, "execute_sql_readonly", []byte(`{"query":"SELECT 1; DELETE FROM ds.t WHERE true; SELECT 2"}`)); v.Allowed || v.Rule != "read-only" {
		t.Fatalf("a DELETE after a SELECT verdict: %+v", v)
	}
	if v := ls.ToolCall(ctx, "list_dataset_ids", []byte(`{"project":"UPDATE"}`)); !v.Allowed {
		t.Fatalf("a tool without SQL was judged as SQL: %+v", v)
	}
	ls.Close(ctx)

	type row struct {
		kind  audit.Kind
		op    inspect.Operation
		text  string
		index string
	}
	var got []row
	for _, r := range judged(sink) {
		got = append(got, row{r.Kind, r.Operation, r.Statement, r.Metadata[inspect.MetadataMCPSQLIndex]})
		if r.Metadata[inspect.MetadataMCPSession] != "m1" || r.Metadata[inspect.MetadataMCPMethod] != "tools/call" {
			t.Errorf("row lacks the call metadata: %+v", r)
		}
		if r.Metadata[inspect.MetadataMCPSQLIndex] != "" &&
			(r.Metadata[inspect.MetadataMCPSQLArgument] != "query" || r.Metadata[inspect.MetadataMCPSQLDialect] != "googlesql") {
			t.Errorf("SQL row lacks its argument and dialect: %+v", r)
		}
	}
	want := []row{
		{audit.KindStatement, inspect.OpCall, "execute_sql\n" + `{"query":"SELECT name FROM ds.customers"}`, ""},
		{audit.KindStatement, inspect.OpSelect, "SELECT name FROM ds.customers", "1"},
		{audit.KindStatement, inspect.OpCall, "execute_sql\n" + `{"query":"UPDATE ds.customers SET name = 'x' WHERE true"}`, ""},
		{audit.KindViolation, inspect.OpUpdate, "UPDATE ds.customers SET name = 'x' WHERE true", "1"},
		{audit.KindStatement, inspect.OpCall, "execute_sql_readonly\n" + `{"query":"SELECT 1; DELETE FROM ds.t WHERE true; SELECT 2"}`, ""},
		{audit.KindStatement, inspect.OpSelect, "SELECT 1", "1"},
		{audit.KindViolation, inspect.OpDelete, "DELETE FROM ds.t WHERE true", "2"},
		{audit.KindStatement, inspect.OpCall, "list_dataset_ids\n" + `{"project":"UPDATE"}`, ""},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("rows\n got %+v\nwant %+v", got, want)
	}
	var counts []string
	for _, r := range judged(sink) {
		if r.Operation == inspect.OpCall {
			counts = append(counts, r.Metadata[inspect.MetadataMCPSQLStatements])
		}
	}
	if want := []string{"1", "1", "3", ""}; !slices.Equal(counts, want) {
		t.Fatalf("mcp.sql_statements on the calls = %q, want %q", counts, want)
	}
}

// A SQL argument the lane cannot read is refused before any rule, with
// rule mcp_gateway.sql, and recorded as mcp_sql_unreadable.
func TestAnMCPSQLArgumentTheLaneCannotReadIsRefused(t *testing.T) {
	ls, sink := openMCPSQLSession(t, `{"rules":[]}`)
	ctx := context.Background()
	for _, tc := range []struct{ name, args, reason string }{
		{"no arguments", ``, "not a JSON object"},
		{"null arguments", `null`, "not a JSON object"},
		{"arguments as a JSON string", `"{\"query\":\"DELETE FROM t\"}"`, "not a JSON object"},
		{"absent", `{"sql":"SELECT 1"}`, `member "query" is absent`},
		{"only in another case", `{"Query":"DELETE FROM t"}`, `differs from it in case`},
		{"twice", `{"query":"SELECT 1","query":"DELETE FROM t"}`, `appears twice`},
		{"twice after unescape", `{"query":"SELECT 1","\u0071uery":"DELETE FROM t"}`, `appears twice`},
		{"again in another case", `{"query":"SELECT 1","QUERY":"DELETE FROM t"}`, `differ only in case`},
		{"not a string", `{"query":["DELETE FROM t"]}`, `not a string`},
		{"blank", `{"query":"  "}`, `is blank`},
		{"no statement", `{"query":" ; ; "}`, `holds no SQL statement`},
		{"lone surrogate", `{"query":"\ud800DELETE FROM t"}`, `lone surrogate`},
	} {
		v := ls.ToolCall(ctx, "execute_sql", []byte(tc.args))
		if v.Allowed || v.Rule != mcpSQLRule || !strings.Contains(v.Message, tc.reason) {
			t.Errorf("%s: %+v, want refused by %s with %q", tc.name, v, mcpSQLRule, tc.reason)
		}
	}
	ls.Close(ctx)
	if rows := judged(sink); len(rows) != 0 {
		t.Fatalf("a refused call reached the rules: %+v", rows)
	}
	var acts int
	for _, r := range sink.Events() {
		if r.Kind != audit.KindActivity || r.Metadata[audit.MetadataActivity] != "mcp_sql_unreadable" {
			continue
		}
		acts++
		if r.Metadata["tool"] != "execute_sql" || r.Metadata["argument"] != "query" || r.Metadata["reason"] == "" {
			t.Errorf("activity: %+v", r.Metadata)
		}
	}
	if acts != 12 {
		t.Fatalf("%d mcp_sql_unreadable activities, want 12", acts)
	}
}

// What a JSON escape decodes to is what the rules read, under the docs rule
// that names unknown.
func TestMCPSQLReadsTheDecodedArgument(t *testing.T) {
	ls, _ := openMCPSQLSession(t, `{"rules":[{"name":"read-only","type":"operation","operations":["insert","update","delete","drop","unknown"]}]}`)
	ctx := context.Background()
	for _, tc := range []struct {
		name, args string
		allowed    bool
	}{
		{"escaped verb", `{"query":"\u0055PDATE t SET a = 1 WHERE true"}`, false},
		{"escaped newline ends a comment", `{"query":"--x\nDELETE FROM t WHERE true"}`, false},
		{"escaped semicolon", `{"query":"SELECT 1\u003b DELETE FROM t WHERE true"}`, false},
		{"escaped quotes", `{"query":"SELECT \u0027a\u0027; DELETE FROM t WHERE true"}`, false},
		{"raw string", `{"query":"SELECT r'\\'; DELETE FROM t WHERE true; --'"}`, false},
		{"carriage return in a comment", `{"query":"--x\rDELETE FROM t WHERE true"}`, false},
		{"U+2028 in a comment", `{"query":"--x\u2028DELETE FROM t WHERE true"}`, false},
		{"a DELETE in a double-quoted string", `{"query":"SELECT \"a\\\"; DELETE FROM t; --\""}`, true},
		{"a FOR loop", `{"query":"FOR r IN (SELECT 1) DO DELETE FROM t WHERE true; END FOR"}`, false},
		{"EXECUTE IMMEDIATE", `{"query":"EXECUTE IMMEDIATE 'DELETE FROM t WHERE true'"}`, false},
		{"a SELECT", `{"query":"SELECT * FROM ds.t"}`, true},
	} {
		if v := ls.ToolCall(ctx, "execute_sql", []byte(tc.args)); v.Allowed != tc.allowed {
			t.Errorf("%s: %+v, want allowed=%v", tc.name, v, tc.allowed)
		}
	}
}

// The longest glob names the argument; two of that length that disagree
// refuse the call.
func TestMCPSQLGlobs(t *testing.T) {
	q := MCPSQLTool{Argument: "query", Dialect: "googlesql"}
	s := MCPSQLTool{Argument: "sql", Dialect: "postgres"}
	tools := map[string]MCPSQLTool{"*": s, "execute_sql*": q, "*_execute_sql": s, "bq-s*": q, "*-sql": s, "pg-*": s}
	for _, tc := range []struct {
		tool string
		want MCPSQLTool
		ok   bool
		err  string
	}{
		{"execute_sql", q, true, ""},
		{"run", s, true, ""},
		{"bigquery_execute_sql", s, true, ""},
		{"bq-sql", MCPSQLTool{}, false, `globs "*-sql" and "bq-s*" both match tool "bq-sql" and disagree`},
		{"pg-sql", s, true, ""},
	} {
		got, ok, err := mcpSQLFor(tools, tc.tool)
		if tc.err != "" {
			if err == nil || err.Error() != "mcp_gateway.sql "+tc.err {
				t.Errorf("%s: err %v, want %q", tc.tool, err, tc.err)
			}
			continue
		}
		if err != nil || ok != tc.ok || got != tc.want {
			t.Errorf("%s: %+v %v %v, want %+v", tc.tool, got, ok, err, tc.want)
		}
	}
	if _, ok, err := mcpSQLFor(nil, "execute_sql"); ok || err != nil {
		t.Fatalf("no sql block: %v %v", ok, err)
	}

	ln := mcpTestLane(t, `{"listeners":[{"name":"m","protocol":"mcp","listen":"127.0.0.1:1","upstream":"https://h/mcp","mcp_gateway":{"sql":{"*-sql":{"argument":"sql","dialect":"postgres"},"bq-s*":{"argument":"query","dialect":"googlesql"}}}}]}`)
	sink := audit.NewMemorySink(16)
	ls, err := openMCPLaneSession(context.Background(), ln, newLiveRules(ln), sink, true, MCPCaller{MCPSessionID: "m1"}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if v := ls.ToolCall(context.Background(), "bq-sql", []byte(`{"query":"SELECT 1","sql":"SELECT 1"}`)); v.Allowed || v.Rule != mcpSQLRule || !strings.Contains(v.Message, "disagree") {
		t.Fatalf("a call two globs disagree on: %+v", v)
	}
}

// guardrails.mode observe judges and records all 1 + N statements.
func TestMCPSQLObserveRecordsEveryStatement(t *testing.T) {
	ls, sink := openMCPSQLSession(t, `{"mode":"observe","rules":[{"name":"read-only","type":"operation","operations":["delete"]}]}`)
	ctx := context.Background()
	if v := ls.ToolCall(ctx, "execute_sql", []byte(`{"query":"DELETE FROM t WHERE true; SELECT 1"}`)); !v.Allowed {
		t.Fatalf("an observing lane denied: %+v", v)
	}
	ls.Close(ctx)
	var wouldDeny []string
	for _, r := range judged(sink) {
		if r.Kind == audit.KindViolation {
			t.Fatalf("an observing lane wrote a violation: %+v", r)
		}
		wouldDeny = append(wouldDeny, r.Metadata[policy.AnnotationWouldDeny])
	}
	if want := []string{"", "read-only", ""}; !slices.Equal(wouldDeny, want) {
		t.Fatalf("would_deny per row = %q, want %q", wouldDeny, want)
	}
}

// A table rule reads a SQL statement as on a SQL lane: require_table_match
// denies one with no tables, and a table the SQL names matches.
func TestMCPSQLTableRules(t *testing.T) {
	ls, _ := openMCPSQLSession(t, `{"rules":[{"name":"no-salaries","type":"table","tables":["salaries"],"require_table_match":true}]}`)
	ctx := context.Background()
	for _, tc := range []struct {
		query   string
		allowed bool
	}{
		{"SELECT * FROM ds.customers", true},
		{"SELECT * FROM hr.salaries", false},
		{"SELECT 1", false},
	} {
		if v := ls.ToolCall(ctx, "execute_sql", []byte(`{"query":"`+tc.query+`"}`)); v.Allowed != tc.allowed {
			t.Errorf("%s: %+v, want allowed=%v", tc.query, v, tc.allowed)
		}
	}
}

// A procedure rule reads the SQL statement under its dialect, and no longer
// reads a call statement's arguments JSON as SQL.
func TestMCPSQLProcedureRules(t *testing.T) {
	ls, _ := openMCPSQLSession(t, `{"rules":[{"name":"no-purge","type":"procedure","procedures":["ds.purge"]}]}`)
	ctx := context.Background()
	if v := ls.ToolCall(ctx, "execute_sql", []byte(`{"query":"CALL ds.purge()"}`)); v.Allowed || v.Rule != "no-purge" {
		t.Fatalf("a CALL in a SQL argument: %+v", v)
	}
	if v := ls.ToolCall(ctx, "execute_sql", []byte(`{"query":"SELECT 'a\\'; CALL ds.purge(); --'"}`)); !v.Allowed {
		t.Fatalf("a CALL in a GoogleSQL string was read under PostgreSQL rules: %+v", v)
	}
	if v := ls.ToolCall(ctx, "search", []byte(`{"text":"CALL ds.purge()"}`)); !v.Allowed {
		t.Fatalf("a call statement was read as SQL: %+v", v)
	}
}
