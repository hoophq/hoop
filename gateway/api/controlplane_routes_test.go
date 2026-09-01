package api

import (
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/apiroutes"
)

// The control plane answers only what this test lists. A route added to
// buildControlPlaneRoutes without being added here fails the test, which is
// the point: routes are ported one at a time and each one is a decision.
func TestControlPlaneRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	a := &Api{}
	a.buildControlPlaneRoutes(apiroutes.New(engine.Group("/api")))

	var got []string
	for _, r := range engine.Routes() {
		got = append(got, r.Method+" "+r.Path)
	}
	sort.Strings(got)

	// apiroutes.Router.GET registers HEAD alongside GET.
	want := []string{"GET /api/healthz", "HEAD /api/healthz"}
	if len(got) != len(want) {
		t.Fatalf("registered routes\ngot:  %v\nwant: %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("route %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

// Liveness must not depend on the gRPC transport: the control plane never
// starts one, so probing it would fail every health check.
func TestControlPlaneHealthzIsOKWithoutGRPC(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	a := &Api{}
	a.buildControlPlaneRoutes(apiroutes.New(engine.Group("/api")))

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/healthz", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d (body=%s)", w.Code, http.StatusOK, w.Body.String())
	}
}
