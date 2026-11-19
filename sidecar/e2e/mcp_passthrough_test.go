//go:build integration

package e2e_test

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpPassthroughConfig is a loopback mcp lane with no caller auth that
// forwards each caller's own upstream credential, with OPA and an analyzer
// on every tool call, so each place the credential could leak to is live.
const mcpPassthroughConfig = `{
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
    "name": "github",
    "protocol": "mcp",
    "listen": "{{listen}}",
    "upstream": "{{upstream}}",
    "analyzer": {"trigger": {"operations": ["call"]}, "high": "block"},
    "mcp_gateway": {"passthrough_header": "X-Hoop-Upstream-Authorization"}
  }]
}`

const mcpUpstreamPAT = "ghp_e2e-upstream-pat-0003"

// mcpPassthroughPost sends one JSON-RPC message with the caller's own
// upstream credential, as an MCP client configured with that header does.
func mcpPassthroughPost(t *testing.T, url, credential, sid, body string) (int, string, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json, text/event-stream")
	if credential != "" {
		req.Header.Set("X-Hoop-Upstream-Authorization", credential)
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

// The shipped binary forwards the caller's own credential to the upstream as
// a bearer and refuses a request without it. The credential reaches neither
// the audit trail, OPA, the analyzer nor the log.
func TestMCPPassthroughCredentialReachesOnlyTheUpstream(t *testing.T) {
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
	cred := filepath.Join(t.TempDir(), "openai-key")
	if err := os.WriteFile(cred, []byte("sk-e2e"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := strings.NewReplacer("{{opa}}", opaURL, "{{model}}", modelURL, "{{credentials}}", cred).Replace(mcpPassthroughConfig)
	logs := &lockedBuffer{}
	// startSidecar binds 0.0.0.0, and a lane with no caller auth listens on loopback only.
	s := startSidecar(t, upURL+"/mcp", config, func(l *sidecarLaunch) {
		l.stderr = logs
		l.body = strings.Replace(l.body, `"listen": "0.0.0.0:`, `"listen": "127.0.0.1:`, 1)
	})
	url := "http://" + s.addr + "/mcp"

	const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"e2e","version":"1"}}}`
	const refused = "request refused: the request carried no X-Hoop-Upstream-Authorization header, the caller's own credential for the MCP server"
	if code, sid, body := mcpPassthroughPost(t, url, "", "", initialize); code != http.StatusForbidden || sid != "" || strings.TrimSpace(body) != refused {
		t.Fatalf("no credential: %d %q, want 403 %q", code, body, refused)
	}
	code, sid, body := mcpPassthroughPost(t, url, "Bearer "+mcpUpstreamPAT, "", initialize)
	if code != http.StatusOK || sid == "" {
		t.Fatalf("initialize: %d %s", code, body)
	}
	mcpPassthroughPost(t, url, "Bearer "+mcpUpstreamPAT, sid, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if code, _, body := mcpPassthroughPost(t, url, mcpUpstreamPAT, sid, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`); code != http.StatusOK || !strings.Contains(body, "echo") {
		t.Fatalf("tools/list: %d %s", code, body)
	}
	if code, _, body := mcpPassthroughPost(t, url, "Bearer "+mcpUpstreamPAT, sid, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"message":"hello from e2e"}}}`); code != http.StatusOK || !strings.Contains(body, "hello from e2e") {
		t.Fatalf("tools/call: %d %s", code, body)
	}
	s.waitForAudit(t, "the echo statement", func(ev auditEvent) bool { return ev.Metadata["mcp.tool"] == "echo" && ev.Allowed })
	time.Sleep(200 * time.Millisecond)

	upN, upText := up.all()
	opaN, opaText := opa.all()
	modelN, modelText := model.all()
	if upN == 0 || opaN == 0 || modelN == 0 || !strings.Contains(logs.String(), "mcp request refused") {
		t.Fatalf("nothing to check: %d upstream requests, %d OPA queries, %d analyzer requests, log %q", upN, opaN, modelN, logs.String())
	}
	if n := strings.Count(upText, "Authorization: Bearer "+mcpUpstreamPAT+"\r\n"); n != upN {
		t.Fatalf("%d of %d upstream requests carry the caller's credential:\n%s", n, upN, upText)
	}
	if strings.Contains(strings.ToLower(upText), "x-hoop-upstream-authorization") {
		t.Fatalf("the passthrough header reached the upstream:\n%s", upText)
	}
	t.Logf("checked %d upstream requests, %d OPA queries, %d analyzer requests, %d audit lines, %d log bytes",
		upN, opaN, modelN, len(s.auditLines()), len(logs.String()))
	for where, text := range map[string]string{
		"the audit trail": strings.Join(s.auditLines(), "\n"),
		"OPA input":       opaText,
		"the analyzer":    modelText,
		"the log":         logs.String(),
	} {
		if strings.Contains(text, mcpUpstreamPAT) {
			t.Errorf("%s holds the credential", where)
		}
	}
}
