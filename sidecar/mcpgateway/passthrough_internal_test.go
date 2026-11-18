package mcpgateway

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hoophq/mcpproxy/auth/outbound"
)

// mcpproxy forwards no inbound header today, so only this test sees the
// middleware remove the passthrough header before mcpproxy reads the request.
func TestThePassthroughHeaderNeverReachesMCPProxy(t *testing.T) {
	s := &server{log: slog.New(slog.DiscardHandler), passthroughHeader: "X-Upstream-Token"}
	var header http.Header
	var credential string
	h := s.passthrough(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		header = r.Header.Clone()
		credential, _ = outbound.ClientTokenFrom(r.Context())
	}))
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("x-upstream-token", "Bearer pat-1")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if header == nil || header.Get("X-Upstream-Token") != "" || credential != "pat-1" {
		t.Fatalf("mcpproxy got header %v and credential %q", header, credential)
	}
}
