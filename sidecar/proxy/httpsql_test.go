package proxy_test

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"strings"
	"testing"

	httpcodec "github.com/hoophq/hoop/sidecar/codec/http"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/lexer"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/proxy"
)

func sqlPost(target, body string) string {
	return fmt.Sprintf("POST %s HTTP/1.1\r\nHost: h\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s",
		target, len(body), body)
}

// sqlLane is an http lane that reads SQL out of Statement Execution API
// bodies, refuses external links and denies DROP.
func sqlLane(t *testing.T, upstream string) *proxy.Server {
	t.Helper()
	return sqlLaneWith(t, upstream, denyDrops(t), []httpcodec.RefuseValue{{Field: "disposition", Value: "EXTERNAL_LINKS"}})
}

func sqlLaneWith(t *testing.T, upstream string, pol policy.Evaluator, refuse []httpcodec.RefuseValue) *proxy.Server {
	t.Helper()
	opts := httpcodec.Options{SQL: &httpcodec.SQLOptions{
		Dialect:       lexer.Spark,
		DialectName:   "spark",
		Fields:        map[string]string{"/api/2.0/sql/statements": "statement"},
		Refuse:        refuse,
		RefuseMessage: "ask for disposition INLINE",
		MatchResource: policy.MatchResource,
	}}
	return startServer(t, proxy.Config{
		Upstream:     upstream,
		Protocol:     inspect.HTTP,
		Policy:       pol,
		DenyWriter:   proxy.ProtocolDenyWriter{},
		CodecFactory: func() inspect.Codec { return httpcodec.New(opts) },
	})
}

// denyWritesAndUnknown is the one guardrail the Databricks docs page shows:
// every write, and what the classifier cannot read.
func denyWritesAndUnknown(t *testing.T) *policy.Rules {
	t.Helper()
	r, err := policy.NewRules([]policy.Rule{{
		Name: "read-only",
		Type: policy.MatchOperation,
		Operations: []inspect.Operation{
			inspect.OpInsert, inspect.OpUpdate, inspect.OpDelete, inspect.OpCreate, inspect.OpDrop,
			inspect.OpAlter, inspect.OpTruncate, inspect.OpGrant, inspect.OpRevoke, inspect.OpUnknown,
		},
		Message: "this listener allows reads only",
	}})
	if err != nil {
		t.Fatalf("NewRules: %v", err)
	}
	return r
}

// A rule naming `unknown` refuses the client statements the classifier
// cannot read and nothing else. Every HTTP response on the relay is unknown
// too (it carries no verb), and on main the rule refused them all: the
// SELECT below came back 403 (ENG-893).
func TestHTTPUnknownRuleJudgesRequestsNotResponses(t *testing.T) {
	up := newRouteUpstream(t, map[string][]byte{
		"/api/2.0/sql/statements": jsonResponse([]byte(`{"result":{"data_array":[["1"]]}}`), ""),
		"/api/2.0/clusters/list":  jsonResponse([]byte(`{"clusters":[]}`), ""),
	})
	srv := sqlLaneWith(t, up.addr(), denyWritesAndUnknown(t), nil)

	for _, raw := range []string{
		sqlPost("/api/2.0/sql/statements", `{"statement":"SELECT * FROM customers"}`),
		"GET /api/2.0/clusters/list HTTP/1.1\r\nHost: h\r\n\r\n",
	} {
		if got := roundTrip(t, srv.Addr().String(), raw); !strings.HasPrefix(got, "200") {
			t.Fatalf("a read's response was refused: %q", got)
		}
	}

	// EXECUTE IMMEDIATE hides its statement in a string: unknown, so the
	// same rule refuses it before it leaves.
	got := roundTrip(t, srv.Addr().String(), sqlPost("/api/2.0/sql/statements",
		`{"statement":"EXECUTE IMMEDIATE 'DROP TABLE customers'"}`))
	if !strings.HasPrefix(got, "403") || !strings.Contains(got, "reads only") {
		t.Fatalf("an unreadable statement was not refused: %q", got)
	}
	if r := up.received(); len(r) != 2 {
		t.Fatalf("upstream received %d requests, want the two reads", len(r))
	}
}

// The SQL in a request body is judged before the request leaves: a DROP is
// answered with the rule's 403 and never reaches the upstream, and a SELECT
// on the same route goes through.
func TestHTTPSQLBodyIsJudgedBeforeItIsForwarded(t *testing.T) {
	up := newRouteUpstream(t, map[string][]byte{
		"/api/2.0/sql/statements": jsonResponse([]byte(`{"status":{"state":"SUCCEEDED"}}`), ""),
	})
	srv := sqlLane(t, up.addr())

	got := roundTrip(t, srv.Addr().String(), sqlPost("/api/2.0/sql/statements",
		`{"statement":"DROP TABLE main.default.customers","warehouse_id":"w"}`))
	if !strings.HasPrefix(got, "403") || !strings.Contains(got, "destructive statements are not permitted") {
		t.Fatalf("DROP was not denied by the rule: %q", got)
	}
	if r := up.received(); len(r) != 0 {
		t.Fatalf("a denied request reached the upstream: %q", r)
	}

	got = roundTrip(t, srv.Addr().String(), sqlPost("/api/2.0/sql/statements",
		`{"statement":"SELECT * FROM customers","warehouse_id":"w"}`))
	if !strings.HasPrefix(got, "200") {
		t.Fatalf("SELECT was not relayed: %q", got)
	}
	if r := up.received(); len(r) != 1 || !strings.Contains(r[0], "SELECT * FROM customers") {
		t.Fatalf("upstream received %q", r)
	}
}

// Padding a body past max_body_bytes does not get its SQL around the lane.
// Its SQL cannot be read, so it is unknown, which the documented rule
// refuses; on a lane that refuses external links it is refused outright,
// since its disposition cannot be checked either. Neither is forwarded.
func TestHTTPSQLRefusesAPaddedBody(t *testing.T) {
	body := `{"statement":"/*` + strings.Repeat("x", 70<<10) + `*/ DROP TABLE customers"}`
	for _, tc := range []struct {
		name string
		srv  func(upstream string) *proxy.Server
		want string
	}{
		{"unknown rule", func(u string) *proxy.Server { return sqlLaneWith(t, u, denyWritesAndUnknown(t), nil) }, "reads only"},
		{"refuse set", func(u string) *proxy.Server { return sqlLane(t, u) }, "max_body_bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			up := newRouteUpstream(t, map[string][]byte{
				"/api/2.0/sql/statements": jsonResponse([]byte(`{}`), ""),
			})
			got := roundTrip(t, tc.srv(up.addr()).Addr().String(), sqlPost("/api/2.0/sql/statements", body))
			if !strings.HasPrefix(got, "403") || !strings.Contains(got, tc.want) {
				t.Fatalf("a padded body was not refused: %.200q", got)
			}
			if r := up.received(); len(r) != 0 {
				t.Fatalf("a refused request reached the upstream")
			}
		})
	}
}

// Databricks routes a path with dot segments or doubled slashes like the
// canonical one, so a DROP sent there is judged like one sent to the
// canonical path, and never forwarded.
func TestHTTPSQLNonCanonicalPathIsJudged(t *testing.T) {
	for _, target := range []string{"/api/2.0/./sql/statements", "/api/2.0//sql/statements", "/api/2.0/sql/x/../statements", `/api/2.0\sql\statements`} {
		t.Run(target, func(t *testing.T) {
			up := newRouteUpstream(t, map[string][]byte{target: jsonResponse([]byte(`{}`), "")})
			got := roundTrip(t, sqlLane(t, up.addr()).Addr().String(), sqlPost(target, `{"statement":"DROP TABLE customers"}`))
			if !strings.HasPrefix(got, "403") || !strings.Contains(got, "destructive statements are not permitted") {
				t.Fatalf("DROP through %s was not denied: %q", target, got)
			}
			if r := up.received(); len(r) != 0 {
				t.Fatalf("a denied request reached the upstream: %q", r)
			}
		})
	}
}

// Masking on an http.sql lane, pinned in the shapes ENG-777 tested against a
// real workspace: a Statement Execution API result and its next chunk (T2),
// gzip on the chunk as Databricks sends it, and an MCP tool result as JSON
// and as an event stream, in both content and structuredContent (T3).
func TestHTTPSQLLaneMasksDatabricksResults(t *testing.T) {
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write([]byte(`{"chunk_index":1,"row_offset":2,"data_array":[["ada@example.com","Ada"]]}`))
	zw.Close()
	toolResult := `{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"[[\"ada@example.com\",\"Ada\"]]"}],` +
		`"structuredContent":{"result":{"data_array":[["ada@example.com","Ada"]]}}}}`
	sse := "event: message\ndata: " + toolResult + "\n\n"
	up := newRouteUpstream(t, map[string][]byte{
		"/api/2.0/sql/statements": jsonResponse([]byte(`{"statement_id":"s","status":{"state":"SUCCEEDED"},`+
			`"result":{"chunk_index":0,"data_array":[["ada@example.com","Ada"],["ada@example.com","Ada"]],`+
			`"next_chunk_internal_link":"/api/2.0/sql/statements/s/result/chunks/1"}}`), ""),
		"/api/2.0/sql/statements/s/result/chunks/1": append([]byte(fmt.Sprintf(
			"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Encoding: gzip\r\nContent-Length: %d\r\n\r\n", gz.Len())), gz.Bytes()...),
		"/ai-gateway/mcp-services/system.ai.dbsql": jsonResponse([]byte(toolResult), ""),
		"/api/2.0/mcp/sql": append([]byte(fmt.Sprintf(
			"HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nContent-Length: %d\r\n\r\n", len(sse))), sse...),
	})
	opts := httpcodec.Options{SQL: &httpcodec.SQLOptions{
		Dialect: lexer.Spark, DialectName: "spark",
		Fields: map[string]string{
			"/api/2.0/sql/statements":     "statement",
			"/ai-gateway/mcp-services/**": "params.arguments.query",
			"/api/2.0/mcp/sql":            "params.arguments.query",
		},
		Tools:         []string{"execute_sql"},
		MatchResource: policy.MatchResource,
	}}
	srv := startServer(t, proxy.Config{
		Upstream:     up.addr(),
		Protocol:     inspect.HTTP,
		Policy:       denyWritesAndUnknown(t),
		Masker:       emailMasker{},
		DenyWriter:   proxy.ProtocolDenyWriter{},
		CodecFactory: func() inspect.Codec { return httpcodec.New(opts) },
	})

	call := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"execute_sql","arguments":{"query":"SELECT * FROM customers"}}}`
	for name, raw := range map[string]string{
		"SEA first chunk":   sqlPost("/api/2.0/sql/statements", `{"statement":"SELECT * FROM customers","disposition":"INLINE"}`),
		"SEA gzip chunk":    "GET /api/2.0/sql/statements/s/result/chunks/1 HTTP/1.1\r\nHost: h\r\n\r\n",
		"MCP JSON result":   sqlPost("/ai-gateway/mcp-services/system.ai.dbsql", call),
		"MCP stream result": sqlPost("/api/2.0/mcp/sql", call),
	} {
		got := roundTrip(t, srv.Addr().String(), raw)
		if status, body, ok := strings.Cut(got, "\n"); ok && strings.HasPrefix(body, "\x1f\x8b") {
			// The lane masks a gzip body inflated and sends it compressed again.
			zr, err := gzip.NewReader(strings.NewReader(body))
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			plain, _ := io.ReadAll(zr)
			got = status + "\n" + string(plain)
		}
		if !strings.HasPrefix(got, "200") || strings.Contains(got, "ada@example.com") || !strings.Contains(got, "[REDACTED]") {
			t.Errorf("%s: not masked: %.300q", name, got)
		}
	}
}

// A request asking for results that skip the lane is refused with the
// operator's message before it is forwarded.
func TestHTTPSQLRefusesExternalLinks(t *testing.T) {
	up := newRouteUpstream(t, map[string][]byte{
		"/api/2.0/sql/statements": jsonResponse([]byte(`{}`), ""),
	})
	srv := sqlLane(t, up.addr())
	got := roundTrip(t, srv.Addr().String(), sqlPost("/api/2.0/sql/statements",
		`{"statement":"SELECT * FROM customers","disposition":"EXTERNAL_LINKS"}`))
	if !strings.HasPrefix(got, "403") || !strings.Contains(got, "ask for disposition INLINE") {
		t.Fatalf("EXTERNAL_LINKS was not refused: %q", got)
	}
	if r := up.received(); len(r) != 0 {
		t.Fatalf("a refused request reached the upstream: %q", r)
	}
}
