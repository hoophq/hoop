//go:build integration

package e2e_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	mcpToken      = "e2e-caller-token-0001"
	mcpWrongToken = "e2e-wrong-token-0002"
)

// mcpTokenConfig is an mcp lane off loopback behind caller_token, with OPA
// and an analyzer configured, so each place a token could leak to is live.
const mcpTokenConfig = `{
  "log_level": "debug",
  "audit": {"file": "-"},
  "opa": {"url": "{{opa}}"},
  "analyzer": {
    "provider": "openai",
    "model": "e2e",
    "endpoint": "{{model}}",
    "credentials_file": "{{credentials}}"
  },
  "listeners": [{
    "name": "tools",
    "protocol": "mcp",
    "listen": "{{listen}}",
    "upstream": "{{upstream}}",
    "caller_token": {"file": "{{tokens}}", "name": "claude-agents"}
  }]
}`

// recorder keeps the head and body of every request a fake server gets.
type recorder struct {
	mu   sync.Mutex
	reqs []string
}

func (rec *recorder) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		var head strings.Builder
		_ = r.Header.Write(&head)
		rec.mu.Lock()
		rec.reqs = append(rec.reqs, head.String()+"\n"+string(body))
		rec.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}

func (rec *recorder) all() (int, string) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return len(rec.reqs), strings.Join(rec.reqs, "\n")
}

func serveRecorded(t *testing.T, rec *recorder, h http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(rec.wrap(h))
	t.Cleanup(srv.Close)
	return srv.URL
}

// lockedBuffer collects the sidecar's log while it writes.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

type echoIn struct {
	Message string `json:"message"`
}

// mcpPost sends one JSON-RPC message to the lane as an MCP client does.
func mcpPost(t *testing.T, url, token, sid, body string) (int, string, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("x-hoop-token", token)
	}
	if sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
		req.Header.Set("MCP-Protocol-Version", "2025-11-25")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Mcp-Session-Id"), string(b)
}

// The shipped binary admits only a listed caller token on an mcp lane bound
// off loopback, and the token reaches neither the audit trail, OPA, the
// analyzer, the log nor the upstream. The trail names the configured caller.
func TestMCPCallerTokenNeverLeavesTheLane(t *testing.T) {
	upstream := sdk.NewServer(&sdk.Implementation{Name: "e2e-upstream", Version: "1"}, nil)
	sdk.AddTool(upstream, &sdk.Tool{Name: "echo", Description: "Echo the message back."},
		func(_ context.Context, _ *sdk.CallToolRequest, in echoIn) (*sdk.CallToolResult, any, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: in.Message}}}, nil, nil
		})
	var up, opa, model recorder
	upURL := serveRecorded(t, &up, sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return upstream }, nil))
	opaURL := serveRecorded(t, &opa, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"result":{"allow":true}}`)
	}))
	modelURL := serveRecorded(t, &model, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "low"}}}})
	}))

	dir := t.TempDir()
	tokens, cred := filepath.Join(dir, "tokens"), filepath.Join(dir, "openai-key")
	if err := os.WriteFile(tokens, []byte(mcpToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cred, []byte("sk-e2e"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := strings.NewReplacer("{{opa}}", opaURL, "{{model}}", modelURL, "{{credentials}}", cred, "{{tokens}}", tokens).
		Replace(mcpTokenConfig)
	logs := &lockedBuffer{}
	s := startSidecar(t, upURL+"/mcp", config, func(l *sidecarLaunch) { l.stderr = logs })
	url := "http://" + s.addr + "/mcp"

	const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"e2e","version":"1"}}}`
	for token, want := range map[string]string{
		"":            "request refused: the request carried no credential",
		mcpWrongToken: "request refused: the caller token is not accepted",
	} {
		if code, sid, body := mcpPost(t, url, token, "", initialize); code != http.StatusForbidden || sid != "" || strings.TrimSpace(body) != want {
			t.Fatalf("token %q: %d %q, want 403 %q", token, code, body, want)
		}
	}

	code, sid, body := mcpPost(t, url, mcpToken, "", initialize)
	if code != http.StatusOK || sid == "" {
		t.Fatalf("initialize with the token: %d %s", code, body)
	}
	mcpPost(t, url, mcpToken, sid, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if code, _, body := mcpPost(t, url, mcpToken, sid, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`); code != http.StatusOK || !strings.Contains(body, "echo") {
		t.Fatalf("tools/list: %d %s", code, body)
	}
	if code, _, body := mcpPost(t, url, mcpToken, sid, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"message":"hello from e2e"}}}`); code != http.StatusOK || !strings.Contains(body, "hello from e2e") {
		t.Fatalf("tools/call: %d %s", code, body)
	}

	call := s.waitForAudit(t, "the echo statement", func(ev auditEvent) bool { return ev.Metadata["mcp.tool"] == "echo" })
	if call.Principal != "claude-agents" || !call.Allowed {
		t.Fatalf("echo statement: %+v, want principal claude-agents and allowed", call)
	}
	// The log is written while the lane runs; give the last lines a moment.
	time.Sleep(200 * time.Millisecond)

	upN, upText := up.all()
	opaN, opaText := opa.all()
	modelN, modelText := model.all()
	if upN == 0 || opaN == 0 || !strings.Contains(logs.String(), "mcp request refused") {
		t.Fatalf("nothing to check: %d upstream requests, %d OPA queries, log %q", upN, opaN, logs.String())
	}
	// An mcp lane calls no analyzer. When one does, its requests join the scan below.
	if modelN != 0 {
		t.Fatalf("the analyzer got %d requests; check them for the token", modelN)
	}
	t.Logf("checked %d upstream requests, %d OPA queries, %d analyzer requests, %d audit lines, %d log bytes",
		upN, opaN, modelN, len(s.auditLines()), len(logs.String()))
	for where, text := range map[string]string{
		"the audit trail": strings.Join(s.auditLines(), "\n"),
		"OPA input":       opaText,
		"the analyzer":    modelText,
		"the log":         logs.String(),
		"the upstream":    upText,
	} {
		for _, tok := range []string{mcpToken, mcpWrongToken} {
			if strings.Contains(text, tok) {
				t.Errorf("%s holds the token %q", where, tok)
			}
		}
	}
	for _, ev := range s.auditEvents() {
		if ev.Principal != "claude-agents" {
			t.Errorf("audit event %s under principal %q, want claude-agents", ev.Kind, ev.Principal)
		}
	}
}

// mcpSQLConfig is the docs BigQuery lane: execute_sql takes GoogleSQL in query.
const mcpSQLConfig = `{
  "audit": {"file": "-"},
  "guardrails": {"rules": [{"name": "read-only", "type": "operation", "operations": ["insert", "update", "delete", "drop", "unknown"], "message": "this lane is read-only"}]},
  "listeners": [{
    "name": "bigquery",
    "protocol": "mcp",
    "listen": "{{listen}}",
    "upstream": "{{upstream}}",
    "caller_token": {"file": "{{tokens}}", "name": "claude-agents"},
    "mcp_gateway": {"sql": {"execute_sql*": {"argument": "query", "dialect": "googlesql"}}}
  }]
}`

type sqlIn struct {
	Query string `json:"query"`
}

// The shipped binary judges the SQL argument of a SQL tool: a SELECT runs, an
// UPDATE and an unreadable argument come back as tool errors and never reach
// the upstream, and the MCP session survives both.
func TestMCPSQLArgumentIsJudged(t *testing.T) {
	var mu sync.Mutex
	var ran []string
	upstream := sdk.NewServer(&sdk.Implementation{Name: "e2e-bigquery", Version: "1"}, nil)
	sdk.AddTool(upstream, &sdk.Tool{Name: "execute_sql", Description: "Run GoogleSQL."},
		func(_ context.Context, _ *sdk.CallToolRequest, in sqlIn) (*sdk.CallToolResult, any, error) {
			mu.Lock()
			ran = append(ran, in.Query)
			mu.Unlock()
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "ran " + in.Query}}}, nil, nil
		})
	var up recorder
	upURL := serveRecorded(t, &up, sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return upstream }, nil))
	tokens := filepath.Join(t.TempDir(), "tokens")
	if err := os.WriteFile(tokens, []byte(mcpToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := startSidecar(t, upURL+"/mcp", strings.NewReplacer("{{tokens}}", tokens).Replace(mcpSQLConfig))
	url := "http://" + s.addr + "/mcp"

	_, sid, body := mcpPost(t, url, mcpToken, "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"e2e","version":"1"}}}`)
	if sid == "" {
		t.Fatalf("initialize: %s", body)
	}
	mcpPost(t, url, mcpToken, sid, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	mcpPost(t, url, mcpToken, sid, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	call := func(id int, args string) string {
		t.Helper()
		code, _, body := mcpPost(t, url, mcpToken, sid, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"execute_sql","arguments":%s}}`, id, args))
		if code != http.StatusOK {
			t.Fatalf("call %d: %d %s", id, code, body)
		}
		t.Logf("call %d %s\n  -> %s", id, args, strings.TrimSpace(body))
		return body
	}

	if body := call(3, `{"query":"SELECT name FROM ds.customers"}`); !strings.Contains(body, "ran SELECT name FROM ds.customers") {
		t.Fatalf("the SELECT did not run: %s", body)
	}
	if body := call(4, `{"query":"\u0055PDATE ds.customers SET name = 'x' WHERE true"}`); !strings.Contains(body, `"isError":true`) ||
		!strings.Contains(body, "denied by the hoop sidecar (rule read-only): this lane is read-only") {
		t.Fatalf("the UPDATE was not refused as a tool error: %s", body)
	}
	if body := call(5, `{"query":"SELECT 1","\u0071uery":"DELETE FROM ds.customers WHERE true"}`); !strings.Contains(body, `"isError":true`) ||
		!strings.Contains(body, "rule mcp_gateway.sql") {
		t.Fatalf("a repeated query key was not refused: %s", body)
	}
	if body := call(6, `{"query":"SELECT 2"}`); !strings.Contains(body, "ran SELECT 2") {
		t.Fatalf("the session did not survive the refusals: %s", body)
	}

	mu.Lock()
	got := append([]string(nil), ran...)
	mu.Unlock()
	if want := []string{"SELECT name FROM ds.customers", "SELECT 2"}; !slices.Equal(got, want) {
		t.Fatalf("the upstream ran %q, want %q", got, want)
	}
	s.waitForAudit(t, "the SELECT statement", func(ev auditEvent) bool {
		return ev.Kind == "statement" && ev.Operation == "select" && ev.Metadata["mcp.sql_index"] == "1" && ev.Metadata["mcp.sql_dialect"] == "googlesql"
	})
	s.waitForAudit(t, "the UPDATE violation", func(ev auditEvent) bool {
		return ev.Kind == "violation" && ev.Operation == "update" && ev.Rule == "read-only" && ev.Statement == "UPDATE ds.customers SET name = 'x' WHERE true"
	})
	s.waitForAudit(t, "the unreadable argument", func(ev auditEvent) bool {
		return ev.Kind == "activity" && ev.Metadata["activity"] == "mcp_sql_unreadable" && strings.Contains(ev.Metadata["reason"], "appears twice")
	})
}
