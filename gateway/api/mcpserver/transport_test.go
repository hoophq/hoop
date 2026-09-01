package mcpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const initializeBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"probe","version":"0"}}}`

func postInitialize(t *testing.T, url, contentType string, extraHeaders map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(initializeBody))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// TestContentTypeSpellings guards the fix for issue #1764: the gateway must
// accept every legal spelling of the JSON Content-Type on POST /api/mcp.
// go-sdk v1.5.0 did a literal string compare and returned 415 for the
// charset-parameterized and case-variant forms that Java MCP clients
// (Apache HttpClient, e.g. AWS Bedrock AgentCore) always send.
func TestContentTypeSpellings(t *testing.T) {
	srv := httptest.NewServer(New(nil).handler)
	defer srv.Close()

	for _, contentType := range []string{
		"application/json",
		"application/json; charset=utf-8",
		"Application/JSON",
	} {
		t.Run(contentType, func(t *testing.T) {
			resp := postInitialize(t, srv.URL, contentType, nil)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("Content-Type %q: got status %d, want 200", contentType, resp.StatusCode)
			}
		})
	}
}

// TestCrossOriginRejected guards the http.NewCrossOriginProtection wrapper in
// New(): go-sdk v1.6.0 stopped enabling Origin verification by default, so
// dropping the wrapper would silently disable CSRF protection. Cross-origin
// non-safe requests must be rejected with 403.
func TestCrossOriginRejected(t *testing.T) {
	srv := httptest.NewServer(New(nil).handler)
	defer srv.Close()

	resp := postInitialize(t, srv.URL, "application/json", map[string]string{
		"Sec-Fetch-Site": "cross-site",
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site POST: got status %d, want 403", resp.StatusCode)
	}
}
