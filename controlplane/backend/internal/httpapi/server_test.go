package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/hoophq/hoop/controlplane/backend/internal/config"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

func discard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// db is nil: no test in this file reaches a handler that touches it. Ready
// does, and is covered by the end-to-end run against a real Postgres rather
// than here, because a fake that answers Ping proves nothing about Ping.
func newTestServer(t *testing.T, cfg config.Config) *gin.Engine {
	t.Helper()
	return New(cfg, nil, discard(), "test").Engine()
}

func do(t *testing.T, engine *gin.Engine, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

// The most important test in the module, and the reason unauthenticatedRoutes
// is a map rather than a paragraph.
//
// It walks what the router actually registered instead of a list written down
// here, so a route added outside the protected group fails this test rather
// than quietly joining the open set.
func TestEveryRouteNotOnTheOpenListIsClosed(t *testing.T) {
	engine := newTestServer(t, config.Config{})

	checked := 0
	for _, r := range engine.Routes() {
		if unauthenticatedRoutes[r.Method+" "+r.Path] {
			continue
		}
		checked++
		t.Run(r.Method+" "+r.Path, func(t *testing.T) {
			// Gin reports the pattern; substitute anything for the parameter.
			path := strings.ReplaceAll(r.Path, ":name", "example")
			rec := do(t, engine, r.Method, path, nil)

			if rec.Code != http.StatusNotImplemented {
				t.Fatalf("status = %d, want 501; a protected route answering anything else is either open or half-built", rec.Code)
			}
			// RequireAdmin runs before the handler, so the ticket in the body
			// is EVL-233's, not the component's. That is what proves the
			// middleware ran rather than the handler.
			if !strings.Contains(rec.Body.String(), "EVL-233") {
				t.Errorf("body = %s, want the admin auth ticket; the request reached the handler without passing RequireAdmin", rec.Body.String())
			}
		})
	}

	if checked == 0 {
		t.Fatal("no protected routes were checked; either the router is empty or unauthenticatedRoutes covers everything")
	}
}

// The open routes have to stay reachable, or the test above passes by locking
// everything including login.
func TestOpenRoutesAreReachable(t *testing.T) {
	engine := newTestServer(t, config.Config{})

	registered := map[string]bool{}
	for _, r := range engine.Routes() {
		registered[r.Method+" "+r.Path] = true
	}
	for route := range unauthenticatedRoutes {
		if !registered[route] {
			t.Errorf("%s is on the open list but is not registered; the list has drifted from the router", route)
		}
	}
}

func TestProbesAnswerWithoutAuth(t *testing.T) {
	engine := newTestServer(t, config.Config{})

	rec := do(t, engine, http.MethodGet, "/healthz", nil)
	if rec.Code != http.StatusOK {
		t.Errorf("/healthz status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"version":"test"`) {
		t.Errorf("/healthz body = %s, want the build version", rec.Body.String())
	}
}

func TestUnknownPathIs404(t *testing.T) {
	engine := newTestServer(t, config.Config{})
	if rec := do(t, engine, http.MethodGet, "/api/nope", nil); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; a catch-all would turn a typo in a service file into an empty 200", rec.Code)
	}
}

func TestSecurityHeadersAreSetOnEveryResponse(t *testing.T) {
	engine := newTestServer(t, config.Config{})
	rec := do(t, engine, http.MethodGet, "/healthz", nil)

	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

func TestCORSAllowsOnlyConfiguredOrigins(t *testing.T) {
	const allowed = "http://localhost:5173"
	engine := newTestServer(t, config.Config{CORSAllowedOrigins: []string{allowed}})

	rec := do(t, engine, http.MethodGet, "/healthz", map[string]string{"Origin": allowed})
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != allowed {
		t.Errorf("Allow-Origin = %q, want %q", got, allowed)
	}

	rec = do(t, engine, http.MethodGet, "/healthz", map[string]string{"Origin": "https://evil.example.com"})
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin = %q for an origin that is not on the list", got)
	}
}

// An empty allow list is the default, and it has to mean nothing is allowed
// rather than everything.
func TestCORSDefaultsToClosed(t *testing.T) {
	engine := newTestServer(t, config.Config{})
	rec := do(t, engine, http.MethodGet, "/healthz", map[string]string{"Origin": "http://localhost:5173"})

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin = %q with an empty allow list", got)
	}
	if got := rec.Header().Get("Vary"); !strings.Contains(got, "Origin") {
		t.Errorf("Vary = %q, want it to contain Origin; a cache that does not vary will serve one origin's response to another", got)
	}
}

// The gateway pairs Allow-Origin: * with Allow-Credentials: true. Browsers
// reject that, and it would be unsafe if they did not.
func TestCORSNeverSendsAWildcard(t *testing.T) {
	engine := newTestServer(t, config.Config{CORSAllowedOrigins: []string{"http://localhost:5173"}})
	rec := do(t, engine, http.MethodGet, "/healthz", map[string]string{"Origin": "http://localhost:5173"})

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got == "*" {
		t.Error("Allow-Origin is a wildcard alongside Allow-Credentials")
	}
}

func TestPreflightIsAnsweredForBothAllowedAndDisallowedOrigins(t *testing.T) {
	engine := newTestServer(t, config.Config{CORSAllowedOrigins: []string{"http://localhost:5173"}})

	for _, origin := range []string{"http://localhost:5173", "https://evil.example.com"} {
		rec := do(t, engine, http.MethodOptions, "/api/login", map[string]string{"Origin": origin})
		if rec.Code != http.StatusNoContent {
			t.Errorf("preflight from %s = %d, want 204; an error status sends people debugging the server instead of the allow list", origin, rec.Code)
		}
	}
}
