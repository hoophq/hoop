package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/hoophq/hoop/controlplane/backend/internal/api/adminauth"
	"github.com/hoophq/hoop/controlplane/backend/internal/api/apierr"
	"github.com/hoophq/hoop/controlplane/backend/internal/api/desiredstate"
	"github.com/hoophq/hoop/controlplane/backend/internal/api/inventory"
	"github.com/hoophq/hoop/controlplane/backend/internal/api/sidecarauth"
	"github.com/hoophq/hoop/controlplane/backend/internal/config"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

func discard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakePinger is why Readiness is an interface. A *gorm.DB cannot be made to
// fail a ping without a real database, so before this the 503 branch of
// /readyz had no test at all.
type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

// testDeps builds the same graph main does, with the real guards.
func testDeps(cfg config.Config) Deps {
	admin := adminauth.New()
	sidecars := sidecarauth.New()
	return Deps{
		Config:           cfg,
		Logger:           discard(),
		Version:          "test",
		Readiness:        fakePinger{},
		RequireAdmin:     admin.RequireAdmin,
		RequireSidecar:   sidecars.RequireSidecar,
		RequireBootstrap: sidecars.RequireBootstrap,
		AdminAuth:        admin,
		DesiredState:     desiredstate.New(),
		Inventory:        inventory.New(),
		SidecarAuth:      sidecars,
	}
}

func newTestServer(t *testing.T, deps Deps) *gin.Engine {
	t.Helper()
	server, err := New(deps)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return server.Engine()
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

// Gin reports route patterns; substitute anything for the parameter.
func concrete(pattern string) string { return strings.ReplaceAll(pattern, ":name", "example") }

// message decodes the one field every error response has.
//
// The tests below compare it whole rather than with strings.Contains. Several
// of the scaffold's descriptions are prefixes of others, "reading a sidecar"
// and "reading a sidecar config" among them, and a substring check would pass
// for a route wired to the wrong one of the two.
func message(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body apierr.Body
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding %s: %v", rec.Body.String(), err)
	}
	return body.Message
}

// guard names the middleware in front of a path, by the description its 501
// carries.
//
// Splitting the surface into /api and /v1 means the guard is derivable from
// the path, so this is one rule per prefix rather than a list of exceptions.
// Enrolment is the single exception, and it is the point: it presents a
// bootstrap credential, so it must not reach the guard that accepts a
// long-lived one.
func guard(path string) string {
	switch {
	case path == "/v1/enroll":
		return "bootstrap credential verification" // sidecarauth.RequireBootstrap
	case strings.HasPrefix(path, "/api/"):
		return "admin authentication" // adminauth.RequireAdmin
	case strings.HasPrefix(path, "/v1/"):
		return "sidecar authentication" // sidecarauth.RequireSidecar
	default:
		return ""
	}
}

// The most important test in the module, and the reason unauthenticatedRoutes
// is a map rather than a paragraph.
//
// It walks what the router actually registered instead of a list written down
// here, so a route added outside a guarded group fails this test rather than
// quietly joining the open set.
func TestEveryRouteNotOnTheOpenListIsClosed(t *testing.T) {
	engine := newTestServer(t, testDeps(config.Config{}))

	checked := 0
	for _, r := range engine.Routes() {
		if unauthenticatedRoutes[r.Method+" "+r.Path] {
			continue
		}
		checked++
		t.Run(r.Method+" "+r.Path, func(t *testing.T) {
			want := guard(r.Path)
			if want == "" {
				t.Fatalf("route is outside both /api and /v1 and is not on the open list, so nothing guards it")
			}

			rec := do(t, engine, r.Method, concrete(r.Path), nil)
			if rec.Code != http.StatusNotImplemented {
				t.Fatalf("status = %d, want 501; a guarded route answering anything else is either open or half-built", rec.Code)
			}
			// The guard runs before the handler, so the description in the
			// body is the guard's, not the component's. That is what proves
			// the middleware ran rather than the handler.
			if got := message(t, rec); got != apierr.NotImplementedMessage(want) {
				t.Errorf("message = %q, want the guard's %q; the request reached the handler without passing it", got, want)
			}
		})
	}

	if checked == 0 {
		t.Fatal("no guarded routes were checked; either the router is empty or unauthenticatedRoutes covers everything")
	}
}

// With the guards stubbed out, every route has to reach its own component.
//
// The test above proves nothing gets past a guard, which means it also cannot
// see a route wired to the wrong handler: everything answers 501 with the
// guard's description either way. This one substitutes pass-through guards,
// which is what Deps carrying the middleware as gin.HandlerFunc buys, and
// asserts the destination.
func TestGuardedRoutesReachTheirOwnComponent(t *testing.T) {
	want := map[string]string{
		"POST /api/auth/logout":                  "admin signout",
		"GET /api/auth/me":                       "reading the current admin",
		"GET /api/sidecars":                      "listing the sidecar fleet",
		"GET /api/sidecars/:name":                "reading a sidecar",
		"DELETE /api/sidecars/:name":             "forgetting a sidecar",
		"GET /api/sidecars/:name/config":         "reading a sidecar config",
		"PUT /api/sidecars/:name/config":         "writing a sidecar config",
		"GET /api/sidecars/:name/status":         "reading a sidecar's reported status",
		"DELETE /api/sidecars/:name/credentials": "sidecar credential revocation",
		"POST /v1/enroll":                        "sidecar enrollment",
		"POST /v1/credentials/rotate":            "sidecar credential rotation",
		"GET /v1/desiredstate":                   "serving a sidecar its desired state",
		"POST /v1/status":                        "recording a sidecar status report",
	}

	// Two handlers sharing a description would make a swap between them
	// invisible to the assertion below, so the distinctness the whole test
	// rests on is checked rather than assumed.
	seen := map[string]string{}
	for route, what := range want {
		if other, dup := seen[what]; dup {
			t.Errorf("%s and %s both answer %q, so this test cannot tell them apart", route, other, what)
		}
		seen[what] = route
	}

	passthrough := func(c *gin.Context) { c.Next() }
	deps := testDeps(config.Config{})
	deps.RequireAdmin = passthrough
	deps.RequireSidecar = passthrough
	deps.RequireBootstrap = passthrough
	engine := newTestServer(t, deps)

	for _, r := range engine.Routes() {
		route := r.Method + " " + r.Path
		if unauthenticatedRoutes[route] {
			continue
		}
		what, listed := want[route]
		if !listed {
			t.Errorf("%s is registered but this table does not say which handler serves it", route)
			continue
		}
		delete(want, route)

		rec := do(t, engine, r.Method, concrete(r.Path), nil)
		if got := message(t, rec); got != apierr.NotImplementedMessage(what) {
			t.Errorf("%s reached the wrong handler: message = %q, want %q", route, got, what)
		}
	}

	for route := range want {
		t.Errorf("%s is in this table but is not registered; the route was renamed or dropped", route)
	}
}

// The open routes have to stay reachable, or the test above passes by locking
// everything including login.
func TestOpenRoutesAreReachable(t *testing.T) {
	engine := newTestServer(t, testDeps(config.Config{}))

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
	engine := newTestServer(t, testDeps(config.Config{}))

	rec := do(t, engine, http.MethodGet, "/healthz", nil)
	if rec.Code != http.StatusOK {
		t.Errorf("/healthz status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"version":"test"`) {
		t.Errorf("/healthz body = %s, want the build version", rec.Body.String())
	}

	if rec := do(t, engine, http.MethodGet, "/readyz", nil); rec.Code != http.StatusOK {
		t.Errorf("/readyz status = %d, want 200 when the database answers", rec.Code)
	}
}

// Readiness failing must shed traffic without asking to be restarted, and
// liveness must not notice at all. Getting this backwards turns a database
// blip into every replica restarting at once.
func TestReadyzIs503WhenTheDatabaseDoesNotAnswerAndHealthzIsNot(t *testing.T) {
	deps := testDeps(config.Config{})
	deps.Readiness = fakePinger{err: errors.New("connection refused")}
	engine := newTestServer(t, deps)

	rec := do(t, engine, http.MethodGet, "/readyz", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("/readyz status = %d, want 503", rec.Code)
	}
	if body := rec.Body.String(); strings.Contains(body, "connection refused") {
		t.Errorf("/readyz body = %s; the driver error must not reach the client, it can carry the connection string", body)
	}

	if rec := do(t, engine, http.MethodGet, "/healthz", nil); rec.Code != http.StatusOK {
		t.Errorf("/healthz status = %d, want 200; a database check in liveness restarts every replica on a blip", rec.Code)
	}
}

// Every field in Deps is one a nil dereference would otherwise find at request
// time, in gin's stack, far from the wiring that was wrong.
func TestNewRefusesIncompleteDeps(t *testing.T) {
	for name, breakIt := range map[string]func(*Deps){
		"Logger":           func(d *Deps) { d.Logger = nil },
		"Readiness":        func(d *Deps) { d.Readiness = nil },
		"RequireAdmin":     func(d *Deps) { d.RequireAdmin = nil },
		"RequireSidecar":   func(d *Deps) { d.RequireSidecar = nil },
		"RequireBootstrap": func(d *Deps) { d.RequireBootstrap = nil },
		"AdminAuth":        func(d *Deps) { d.AdminAuth = nil },
		"DesiredState":     func(d *Deps) { d.DesiredState = nil },
		"Inventory":        func(d *Deps) { d.Inventory = nil },
		"SidecarAuth":      func(d *Deps) { d.SidecarAuth = nil },
	} {
		t.Run(name, func(t *testing.T) {
			deps := testDeps(config.Config{})
			breakIt(&deps)

			server, err := New(deps)
			if err == nil {
				t.Fatalf("New succeeded with %s missing", name)
			}
			if server != nil {
				t.Error("New returned a Server alongside an error")
			}
			if !strings.Contains(err.Error(), name) {
				t.Errorf("error = %q, want it to name %s", err, name)
			}
		})
	}
}

func TestUnknownPathIs404(t *testing.T) {
	engine := newTestServer(t, testDeps(config.Config{}))
	if rec := do(t, engine, http.MethodGet, "/api/nope", nil); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; a catch-all would turn a typo in a service file into an empty 200", rec.Code)
	}
}

func TestSecurityHeadersAreSetOnEveryResponse(t *testing.T) {
	engine := newTestServer(t, testDeps(config.Config{}))
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
	engine := newTestServer(t, testDeps(config.Config{CORSAllowedOrigins: []string{allowed}}))

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
	engine := newTestServer(t, testDeps(config.Config{}))
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
	engine := newTestServer(t, testDeps(config.Config{CORSAllowedOrigins: []string{"http://localhost:5173"}}))
	rec := do(t, engine, http.MethodGet, "/healthz", map[string]string{"Origin": "http://localhost:5173"})

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got == "*" {
		t.Error("Allow-Origin is a wildcard alongside Allow-Credentials")
	}
}

func TestPreflightIsAnsweredForBothAllowedAndDisallowedOrigins(t *testing.T) {
	engine := newTestServer(t, testDeps(config.Config{CORSAllowedOrigins: []string{"http://localhost:5173"}}))

	for _, origin := range []string{"http://localhost:5173", "https://evil.example.com"} {
		rec := do(t, engine, http.MethodOptions, "/api/auth/login", map[string]string{"Origin": origin})
		if rec.Code != http.StatusNoContent {
			t.Errorf("preflight from %s = %d, want 204; an error status sends people debugging the server instead of the allow list", origin, rec.Code)
		}
	}
}
