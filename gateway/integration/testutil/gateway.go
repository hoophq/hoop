//go:build integration

package testutil

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// GatewayTestServer wraps an httptest.Server running the gateway's full gin
// route tree against a real PostgreSQL-backed model layer. Tests drive it
// with the typed HTTP helpers below; every request targets the "/api" base
// path the real gateway serves under.
type GatewayTestServer struct {
	// BaseURL is the httptest server root, e.g. "http://127.0.0.1:PORT".
	BaseURL string

	server *httptest.Server
	client *http.Client
}

// NewGatewayTestServer wraps an already-built gin handler in an httptest
// server. The handler must already have all routes registered under "/api".
func NewGatewayTestServer(handler http.Handler) *GatewayTestServer {
	srv := httptest.NewServer(handler)
	return &GatewayTestServer{
		BaseURL: srv.URL,
		server:  srv,
		client: &http.Client{
			Timeout: 30 * time.Second,
			// Smoke tests assert on raw status codes (401/403/404); never
			// follow redirects so an auth bounce isn't masked as a 200.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Close shuts down the underlying httptest server.
func (s *GatewayTestServer) Close() {
	if s.server != nil {
		s.server.Close()
	}
}

// apiURL builds an absolute URL for an /api path. The path may or may not
// include a leading slash.
func (s *GatewayTestServer) apiURL(path string) string {
	return s.BaseURL + "/api/" + strings.TrimPrefix(path, "/")
}

// do issues a request to an /api path. token, when non-empty, is sent as a
// Bearer Authorization header (works for both local JWTs and hpk_ API keys).
// body, when non-nil, is JSON-encoded. The caller owns the returned response
// body and must close it.
func (s *GatewayTestServer) do(t *testing.T, method, path, token string, body any) *http.Response {
	t.Helper()

	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("gateway test: marshal request body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(context.Background(), method, s.apiURL(path), reader)
	if err != nil {
		t.Fatalf("gateway test: build request %s %s: %v", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		t.Fatalf("gateway test: %s %s: %v", method, path, err)
	}
	return resp
}

// Get issues an authenticated (or anonymous, when token=="") GET.
func (s *GatewayTestServer) Get(t *testing.T, path, token string) *http.Response {
	t.Helper()
	return s.do(t, http.MethodGet, path, token, nil)
}

// Post issues a POST with a JSON body.
func (s *GatewayTestServer) Post(t *testing.T, path, token string, body any) *http.Response {
	t.Helper()
	return s.do(t, http.MethodPost, path, token, body)
}

// Put issues a PUT with a JSON body.
func (s *GatewayTestServer) Put(t *testing.T, path, token string, body any) *http.Response {
	t.Helper()
	return s.do(t, http.MethodPut, path, token, body)
}

// Delete issues a DELETE.
func (s *GatewayTestServer) Delete(t *testing.T, path, token string) *http.Response {
	t.Helper()
	return s.do(t, http.MethodDelete, path, token, nil)
}

// Body-lifecycle convention for this package: the code that obtains a
// *http.Response owns closing its body (via defer right after the call). The
// helpers below only read; they never close. This keeps a single, obvious
// ownership rule and avoids double-close.

// DecodeJSON unmarshals a response body into out. It does not close the body.
func DecodeJSON(t *testing.T, resp *http.Response, out any) {
	t.Helper()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatalf("gateway test: decode response body: %v", err)
	}
}

// ReadBody reads and returns the response body as a string. It does not close
// the body. Useful for asserting error messages or debugging status codes.
func ReadBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("gateway test: read response body: %v", err)
	}
	return string(raw)
}

// RequireStatus fails the test if the response status does not match want,
// including the response body in the failure message for diagnosis. It does
// not close the body.
func RequireStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		t.Fatalf("gateway test: expected status %d, got %d (body: %s)", want, resp.StatusCode, ReadBody(t, resp))
	}
}
