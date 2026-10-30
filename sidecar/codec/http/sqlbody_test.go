package http_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	codechttp "github.com/hoophq/hoop/sidecar/codec/http"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/lexer"
	"github.com/hoophq/hoop/sidecar/policy"
)

// databricksSQL is the http.sql block the Databricks setup configures: the
// Statement Execution API and the workspace SQL MCP server.
func databricksSQL() *codechttp.SQLOptions {
	return &codechttp.SQLOptions{
		Dialect:     lexer.Spark,
		DialectName: "spark",
		Fields: map[string]string{
			"/api/2.0/sql/statements":     "statement",
			"/ai-gateway/mcp-services/**": "params.arguments.query",
			"/api/2.0/mcp/sql":            "params.arguments.query",
		},
		Tools: []string{"execute_sql"},
		Refuse: []codechttp.RefuseValue{
			{Field: "disposition", Value: "EXTERNAL_LINKS"},
			{Field: "disposition", Value: "INLINE_OR_EXTERNAL_LINKS"},
		},
		RefuseMessage: "ask for disposition INLINE: external links skip this listener",
		MatchResource: policy.MatchResource,
	}
}

func post(target, body string) string {
	return fmt.Sprintf("POST %s HTTP/1.1\r\nHost: h\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s",
		target, len(body), body)
}

func decodeClient(t *testing.T, c *codechttp.Codec, raw string) ([]inspect.Statement, error) {
	t.Helper()
	stmts, n, err := c.Decode(inspect.FromClient, []byte(raw))
	if err == nil && n != len(raw) {
		t.Fatalf("consumed %d of %d bytes", n, len(raw))
	}
	return stmts, err
}

func mustDecode(t *testing.T, c *codechttp.Codec, raw string) []inspect.Statement {
	t.Helper()
	stmts, err := decodeClient(t, c, raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	return stmts
}

// The Statement Execution API carries its SQL in `statement`. The request is
// still a statement of its own, and the SQL follows it, classified, with no
// HTTP detail, so operation and table rules read the SQL and http_resource
// rules read the request.
func TestSQLBodyStatementFollowsItsRequest(t *testing.T) {
	c := codechttp.New(codechttp.Options{SQL: databricksSQL()})
	stmts := mustDecode(t, c, post("/api/2.0/sql/statements",
		`{"statement":"DROP TABLE main.default.customers","warehouse_id":"w","disposition":"INLINE"}`))
	if len(stmts) != 2 {
		t.Fatalf("got %d statements, want the request and its SQL: %+v", len(stmts), stmts)
	}
	req, sql := stmts[0], stmts[1]
	if req.HTTP == nil || req.HTTP.Method != "POST" || req.Metadata[codechttp.MetadataSQLStatements] != "1" {
		t.Errorf("request statement = %+v", req)
	}
	if sql.HTTP != nil || sql.Protocol != inspect.HTTP || sql.Direction != inspect.FromClient {
		t.Errorf("SQL statement must be an http statement without HTTP detail: %+v", sql)
	}
	if sql.Text != "DROP TABLE main.default.customers" || sql.Operation != inspect.OpDrop {
		t.Errorf("SQL statement text %q operation %s", sql.Text, sql.Operation)
	}
	for k, want := range map[string]string{
		codechttp.MetadataSQLIndex:    "1",
		codechttp.MetadataSQLField:    "statement",
		codechttp.MetadataSQLDialect:  "spark",
		codechttp.MetadataSQLResource: "/api/2.0/sql/statements",
	} {
		if got := sql.Metadata[k]; got != want {
			t.Errorf("metadata %s = %q, want %q", k, got, want)
		}
	}
}

// The workspace SQL MCP server carries SQL in a tools/call to execute_sql.
// Every other JSON-RPC message on the same route runs no SQL.
func TestSQLBodyMCPToolCall(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       inspect.Operation // "" when no SQL statement follows
	}{
		{"execute_sql", `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"execute_sql",` +
			`"arguments":{"query":"DELETE FROM customers WHERE 1=1"}}}`, inspect.OpDelete},
		{"initialize", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`, ""},
		{"another tool", `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"poll_sql_result",` +
			`"arguments":{"statement_id":"s"}}}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := codechttp.New(codechttp.Options{SQL: databricksSQL()})
			stmts := mustDecode(t, c, post("/ai-gateway/mcp-services/system.ai.dbsql", tc.body))
			if tc.want == "" {
				if len(stmts) != 1 {
					t.Fatalf("a message that runs no SQL produced %d statements", len(stmts))
				}
				return
			}
			if len(stmts) != 2 || stmts[1].Operation != tc.want {
				t.Fatalf("statements = %+v, want the request and a %s", stmts, tc.want)
			}
			if got := stmts[1].Metadata[codechttp.MetadataSQLField]; got != "params.arguments.query" {
				t.Errorf("field = %q", got)
			}
		})
	}
}

// A JSON-RPC batch carries one SQL statement per tools/call, numbered.
func TestSQLBodyJSONRPCBatch(t *testing.T) {
	c := codechttp.New(codechttp.Options{SQL: databricksSQL()})
	call := func(id int, q string) string {
		return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"execute_sql","arguments":{"query":%q}}}`, id, q)
	}
	stmts := mustDecode(t, c, post("/api/2.0/mcp/sql",
		"["+call(1, "SELECT 1")+`,{"jsonrpc":"2.0","method":"notifications/initialized"},`+call(2, "DROP TABLE t")+"]"))
	if len(stmts) != 3 {
		t.Fatalf("got %d statements, want the request and two SQL statements", len(stmts))
	}
	if stmts[1].Operation != inspect.OpSelect || stmts[1].Metadata[codechttp.MetadataSQLIndex] != "1" ||
		stmts[2].Operation != inspect.OpDrop || stmts[2].Metadata[codechttp.MetadataSQLIndex] != "2" {
		t.Fatalf("batch statements = %+v", stmts[1:])
	}
	if stmts[0].Metadata[codechttp.MetadataSQLStatements] != "2" {
		t.Errorf("request metadata = %v", stmts[0].Metadata)
	}
}

// A request asking for results the lane cannot see is refused with the
// operator's message, whatever the lane's rules: ErrStreamUnsafe, which the
// gate denies regardless of policy. The value compares case-insensitively.
func TestSQLBodyRefusesExternalResults(t *testing.T) {
	for _, disposition := range []string{"EXTERNAL_LINKS", "external_links", "INLINE_OR_EXTERNAL_LINKS"} {
		t.Run(disposition, func(t *testing.T) {
			c := codechttp.New(codechttp.Options{SQL: databricksSQL()})
			_, err := decodeClient(t, c, post("/api/2.0/sql/statements",
				`{"statement":"SELECT * FROM customers","warehouse_id":"w","disposition":"`+disposition+`"}`))
			if !errors.Is(err, inspect.ErrStreamUnsafe) {
				t.Fatalf("err = %v, want ErrStreamUnsafe", err)
			}
			for _, want := range []string{"ask for disposition INLINE", "disposition is " + disposition} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q does not say %q", err, want)
				}
			}
		})
	}
	c := codechttp.New(codechttp.Options{SQL: databricksSQL()})
	if stmts := mustDecode(t, c, post("/api/2.0/sql/statements",
		`{"statement":"SELECT 1","disposition":"INLINE"}`)); len(stmts) != 2 {
		t.Fatalf("an INLINE request was not read: %+v", stmts)
	}
}

var truncatedDrop = `{"statement":"SELECT 1 /*` + strings.Repeat("x", 200) + `*/ ; DROP TABLE t"}`

// A body that should carry SQL and does not yield it becomes an OpUnknown
// statement with the reason recorded, as every codec reports SQL it cannot
// read, so a rule naming `unknown` refuses it rather than letting a
// malformed or oversized body pass as a plain POST.
func TestSQLBodyUnreadableIsUnknown(t *testing.T) {
	noRefuse := databricksSQL()
	noRefuse.Refuse = nil
	for _, tc := range []struct {
		name, body string
		maxBody    int
		want       string
	}{
		{"not JSON", `DROP TABLE t`, 0, "not JSON"},
		{"no statement", `{"warehouse_id":"w"}`, 0, `no SQL string at "statement"`},
		{"statement is not a string", `{"statement":42}`, 0, `no SQL string at "statement"`},
		{"not an object", `[1,2]`, 0, "not an object"},
		{"truncated", truncatedDrop, 64, "http.max_body_bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := codechttp.New(codechttp.Options{SQL: noRefuse, MaxBodyBytes: tc.maxBody})
			stmts := mustDecode(t, c, post("/api/2.0/sql/statements", tc.body))
			if len(stmts) < 2 {
				t.Fatalf("got %d statements, want the request and an unknown one", len(stmts))
			}
			sql := stmts[1]
			if sql.Operation != inspect.OpUnknown || !strings.Contains(sql.Metadata[inspect.MetadataSQLIncomplete], tc.want) {
				t.Fatalf("statement = %+v, want unknown with a reason naming %q", sql, tc.want)
			}
			if sql.Metadata[codechttp.MetadataSQLField] != "statement" || sql.HTTP != nil {
				t.Errorf("statement = %+v", sql)
			}
		})
	}
}

// A lane that refuses external results refuses a body it cannot read at all,
// since it cannot check the body's disposition: padding past max_body_bytes
// must not get an EXTERNAL_LINKS request past the refusal. A readable body
// without SQL is still only unknown.
func TestSQLBodyUnparseableIsRefusedWhenRefuseIsSet(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		maxBody    int
		want       string
	}{
		{"not JSON", `DROP TABLE t`, 0, "not JSON"},
		{"truncated", truncatedDrop, 64, "http.max_body_bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := codechttp.New(codechttp.Options{SQL: databricksSQL(), MaxBodyBytes: tc.maxBody})
			_, err := decodeClient(t, c, post("/api/2.0/sql/statements", tc.body))
			if !errors.Is(err, inspect.ErrStreamUnsafe) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want a refusal naming %q", err, tc.want)
			}
		})
	}
	c := codechttp.New(codechttp.Options{SQL: databricksSQL()})
	stmts := mustDecode(t, c, post("/api/2.0/sql/statements", `{"warehouse_id":"w"}`))
	if len(stmts) != 2 || stmts[1].Operation != inspect.OpUnknown {
		t.Fatalf("a readable body without SQL: %+v", stmts)
	}
}

// Only the configured resources are read. A body with a `statement` member
// on another route is left alone.
func TestSQLBodyOtherResourcesUntouched(t *testing.T) {
	c := codechttp.New(codechttp.Options{SQL: databricksSQL()})
	stmts := mustDecode(t, c, post("/api/2.0/clusters/list", `{"statement":"DROP TABLE t"}`))
	if len(stmts) != 1 || stmts[0].Metadata[codechttp.MetadataSQLStatements] != "" {
		t.Fatalf("statements = %+v", stmts)
	}
}

// The lane's dialect reads the SQL. In Spark a `--` comment continues past a
// backslash-newline, so the DROP below is live; read as Postgres it hides in
// a string literal.
func TestSQLBodyUsesTheLaneDialect(t *testing.T) {
	hidden := "-- note \\\nSELECT '\nDROP TABLE customers -- '"
	body := fmt.Sprintf(`{"statement":%q}`, hidden)
	for _, tc := range []struct {
		dialect lexer.Dialect
		want    inspect.Operation
	}{{lexer.Spark, inspect.OpDrop}, {lexer.Postgres, inspect.OpSelect}} {
		o := databricksSQL()
		o.Dialect = tc.dialect
		c := codechttp.New(codechttp.Options{SQL: o})
		if stmts := mustDecode(t, c, post("/api/2.0/sql/statements", body)); stmts[1].Operation != tc.want {
			t.Errorf("dialect %v read %s, want %s", tc.dialect, stmts[1].Operation, tc.want)
		}
	}
}

// A specific route wins over a "/**" that also covers it.
func TestSQLBodyLongestPatternWins(t *testing.T) {
	o := databricksSQL()
	o.Fields = map[string]string{"/api/**": "query", "/api/2.0/sql/statements": "statement"}
	c := codechttp.New(codechttp.Options{SQL: o})
	stmts := mustDecode(t, c, post("/api/2.0/sql/statements", `{"statement":"DROP TABLE t","query":"SELECT 1"}`))
	if stmts[1].Operation != inspect.OpDrop {
		t.Fatalf("read %q from %s", stmts[1].Text, stmts[1].Metadata[codechttp.MetadataSQLField])
	}
}

// Two patterns of the same length leave the choice to their spelling. When
// both match and name different paths, the request is refused, not read at
// one path while a DROP waits at the other. A tie on one path, a request
// only one of them matches and a request without a body pass as before.
func TestSQLBodyTiedPatternsNamingTwoPathsRefuse(t *testing.T) {
	o := databricksSQL()
	o.Fields = map[string]string{"/api/*/sql/x": "query", "/api/2.0/*/x": "statement"}
	c := codechttp.New(codechttp.Options{SQL: o})
	_, err := decodeClient(t, c, post("/api/2.0/sql/x", `{"query":"SELECT 1","statement":"DROP TABLE t"}`))
	if !errors.Is(err, inspect.ErrStreamUnsafe) || !strings.Contains(err.Error(), `"/api/*/sql/x" and "/api/2.0/*/x"`) {
		t.Fatalf("err = %v, want the tie refused", err)
	}

	c = codechttp.New(codechttp.Options{SQL: o})
	if stmts := mustDecode(t, c, post("/api/9.9/sql/x", `{"query":"DROP TABLE t"}`)); len(stmts) != 2 || stmts[1].Operation != inspect.OpDrop {
		t.Errorf("a request one pattern matches: %+v", stmts)
	}
	c = codechttp.New(codechttp.Options{SQL: o})
	if stmts := mustDecode(t, c, "GET /api/2.0/sql/x HTTP/1.1\r\nHost: h\r\n\r\n"); len(stmts) != 1 {
		t.Errorf("a request without a body: %+v", stmts)
	}

	o.Fields = map[string]string{"/api/*/sql/x": "statement", "/api/2.0/*/x": "statement"}
	c = codechttp.New(codechttp.Options{SQL: o})
	if stmts := mustDecode(t, c, post("/api/2.0/sql/x", `{"statement":"DROP TABLE t"}`)); len(stmts) != 2 || stmts[1].Operation != inspect.OpDrop {
		t.Errorf("a tie on one path: %+v", stmts)
	}
}

// A JSON-RPC member on the Statement Execution route decides nothing: the API
// runs such a body (checked on a workspace, 2026-10-29), so its DROP is read
// like any other.
func TestSQLBodyJSONRPCMembersOnAPlainRoute(t *testing.T) {
	for _, body := range []string{
		`{"jsonrpc":1,"statement":"DROP TABLE t","warehouse_id":"w"}`,
		`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"list_tables"},"statement":"DROP TABLE t"}`,
	} {
		c := codechttp.New(codechttp.Options{SQL: databricksSQL()})
		if stmts := mustDecode(t, c, post("/api/2.0/sql/statements", body)); len(stmts) != 2 || stmts[1].Operation != inspect.OpDrop {
			t.Errorf("%s: statements = %+v, want the request and its DROP", body, stmts)
		}
	}
}

// A WebSocket message on a SQL route carries its handshake's method and
// resource, but its payload is a frame, not a request body: it is not read
// for SQL, so a payload that is not JSON is not refused, and it keeps no body
// the lane captured only to look for SQL.
func TestSQLBodyIgnoresWebSocketMessages(t *testing.T) {
	o := databricksSQL()
	o.Fields = map[string]string{"/ws/**": "statement"}
	c := codechttp.New(codechttp.Options{SQL: o})
	mustDecode(t, c, "GET /ws/sql HTTP/1.1\r\nHost: h\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")
	if _, _, err := c.Decode(inspect.FromServer, []byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\n"+
		"Connection: Upgrade\r\nSec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\n\r\n")); err != nil {
		t.Fatalf("101: %v", err)
	}
	for _, payload := range []string{"not json", `{"statement":"DROP TABLE t"}`} {
		frame := []byte{0x81, 0x80 | byte(len(payload)), 1, 2, 3, 4}
		for i := range len(payload) {
			frame = append(frame, payload[i]^byte(i%4+1))
		}
		stmts, err := decodeClient(t, c, string(frame))
		if err != nil {
			t.Fatalf("%s: a WebSocket message was refused: %v", payload, err)
		}
		if len(stmts) != 1 || stmts[0].HTTP == nil || stmts[0].HTTP.WebSocket == nil {
			t.Fatalf("%s: statements = %+v, want the WebSocket message only", payload, stmts)
		}
		if stmts[0].HTTP.Body != "" {
			t.Errorf("%s: the message kept a body the lane captured only for SQL: %q", payload, stmts[0].HTTP.Body)
		}
	}
}

// The SQL statement carries no credential handle: the gate resolves the
// caller on the request statement, and a handle on the SQL statement would be
// a second resolution for one request.
func TestSQLStatementCarriesNoCredential(t *testing.T) {
	c := codechttp.New(codechttp.Options{SQL: databricksSQL(), CredentialHeader: "x-caller"})
	raw := strings.Replace(post("/api/2.0/sql/statements", `{"statement":"SELECT 1"}`),
		"Host: h\r\n", "Host: h\r\nX-Caller: alice\r\n", 1)
	stmts := mustDecode(t, c, raw)
	if cred, ok := c.TakeCredential(&stmts[0]); !ok || cred != "alice" {
		t.Errorf("request credential = %q, %v", cred, ok)
	}
	if _, ok := c.TakeCredential(&stmts[1]); ok {
		t.Errorf("the SQL statement carries a credential handle: %v", stmts[1].Metadata)
	}
}

// Databricks resolves dot segments and doubled or trailing slashes before
// it routes, so each of these runs a statement like the canonical path. The
// lane reads the SQL out of all of them.
func TestSQLBodyReadsNonCanonicalPaths(t *testing.T) {
	for _, target := range []string{
		"/api/2.0/./sql/statements",
		"/api/2.0//sql/statements",
		"/api/2.0/sql/x/../statements",
		"/api/2.0/sql/statements/.",
		"/api/2.0/sql/statements/",
		"//api/2.0/sql/statements",
		"/api/2.0/%2e/sql/statements",
		`/api/2.0\sql\statements`,
		`/api/2.0/sql/statements\`,
		`/api/2.0/sql/./statements\.`,
		"/api/2.0%5csql%5cstatements",
		"/ai-gateway/./mcp-services/system.ai.dbsql",
		`/ai-gateway\mcp-services\system.ai.dbsql`,
	} {
		t.Run(target, func(t *testing.T) {
			body := `{"statement":"DROP TABLE t"}`
			if strings.Contains(target, "mcp") {
				body = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"execute_sql","arguments":{"query":"DROP TABLE t"}}}`
			}
			c := codechttp.New(codechttp.Options{SQL: databricksSQL()})
			stmts := mustDecode(t, c, post(target, body))
			if len(stmts) != 2 || stmts[1].Operation != inspect.OpDrop {
				t.Fatalf("statements = %+v, want the request and its DROP", stmts)
			}
		})
	}
}

// Databricks takes an enum by its number too: "disposition": 2 is
// EXTERNAL_LINKS. A refuse field holding anything but a string cannot be
// compared, so the request is refused. Null is the server's default.
func TestSQLBodyRefusesANonStringRefuseValue(t *testing.T) {
	for _, v := range []string{`2`, `true`, `["EXTERNAL_LINKS"]`, `{"x":1}`} {
		t.Run(v, func(t *testing.T) {
			c := codechttp.New(codechttp.Options{SQL: databricksSQL()})
			_, err := decodeClient(t, c, post("/api/2.0/sql/statements", `{"statement":"SELECT 1","disposition":`+v+`}`))
			if !errors.Is(err, inspect.ErrStreamUnsafe) || !strings.Contains(err.Error(), "disposition is not a string") {
				t.Fatalf("err = %v, want a refusal", err)
			}
		})
	}
	for _, v := range []string{`null`, `"INLINE"`} {
		c := codechttp.New(codechttp.Options{SQL: databricksSQL()})
		if stmts := mustDecode(t, c, post("/api/2.0/sql/statements", `{"statement":"SELECT 1","disposition":`+v+`}`)); len(stmts) != 2 {
			t.Fatalf("disposition %s was not read: %+v", v, stmts)
		}
	}
}

// The lane captures request bodies to find SQL. A body sent to any other
// route is dropped from its statement, so a secret posted to another API
// reaches neither the audit trail nor an analyzer, unless the operator
// asked for bodies. A SQL route keeps its body.
func TestSQLBodyKeepsOnlyTheBodiesOfItsRoutes(t *testing.T) {
	secret := `{"scope":"s","key":"k","string_value":"not-for-the-audit"}`
	c := codechttp.New(codechttp.Options{SQL: databricksSQL()})
	stmts := mustDecode(t, c, post("/api/2.0/secrets/put", secret))
	if len(stmts) != 1 || stmts[0].HTTP.Body != "" {
		t.Fatalf("a body to another route was kept: %+v", stmts[0].HTTP)
	}
	stmts = mustDecode(t, c, post("/api/2.0/sql/statements", `{"statement":"SELECT 1"}`))
	if !strings.Contains(stmts[0].HTTP.Body, "SELECT 1") {
		t.Errorf("the SQL route lost its body: %+v", stmts[0].HTTP)
	}

	for name, o := range map[string]codechttp.Options{
		"capture_body":      {SQL: databricksSQL(), CaptureBody: true},
		"a lane that holds": {SQL: databricksSQL(), CaptureRequestBody: true},
	} {
		c := codechttp.New(o)
		if stmts := mustDecode(t, c, post("/api/2.0/secrets/put", secret)); !strings.Contains(stmts[0].HTTP.Body, "not-for-the-audit") {
			t.Errorf("%s: the operator's capture was dropped", name)
		}
	}
}

// A request with no body runs no SQL: an MCP client opens its event stream
// with a GET on the same route. It passes as a plain request, not refused
// for a body that is not JSON.
func TestSQLBodyRequestWithoutABodyCarriesNoSQL(t *testing.T) {
	c := codechttp.New(codechttp.Options{SQL: databricksSQL()})
	stmts, err := decodeClient(t, c, "GET /ai-gateway/mcp-services/system.ai.dbsql HTTP/1.1\r\nHost: h\r\nAccept: text/event-stream\r\n\r\n")
	if err != nil || len(stmts) != 1 {
		t.Fatalf("a bodyless GET: %d statements, %v", len(stmts), err)
	}
}
