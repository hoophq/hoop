package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/hoophq/hoop/controlplane/backend/internal/config"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(config.Config{}, logger, "test").Handler()
}

func do(t *testing.T, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	newTestServer(t).ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestHealthzReportsUpAndVersion(t *testing.T) {
	rec := do(t, http.MethodGet, "/healthz")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding %s: %v", rec.Body.String(), err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want %q", body.Status, "ok")
	}
	if body.Version != "test" {
		t.Errorf("version = %q, want %q", body.Version, "test")
	}
}

// TestUnknownRouteIs404 pins the absence of a NoRoute handler. This binary
// serves no UI, so an unmatched path must look like a mistake.
func TestUnknownRouteIs404(t *testing.T) {
	if rec := do(t, http.MethodGet, "/nope"); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// TestOnlyHealthzIsRegistered fails when a route is added without a test. The
// scaffold serves one route on purpose, so growth here should be deliberate.
func TestOnlyHealthzIsRegistered(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	routes := New(config.Config{}, logger, "test").engine.Routes()

	if len(routes) != 1 {
		t.Fatalf("got %d routes, want 1: %v", len(routes), routes)
	}
	if got := routes[0].Method + " " + routes[0].Path; got != "GET /healthz" {
		t.Errorf("route = %q, want %q", got, "GET /healthz")
	}
}
