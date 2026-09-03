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
	// The surface the shipped controlplane/frontend calls, measured from its
	// service files and verified endpoint by endpoint against a running
	// gateway. Adding a route to buildControlPlaneRoutes without adding it
	// here fails this test, which is the point.
	want := []string{
		"DELETE /api/access-requests/rules/:name",
		"DELETE /api/ai/session-analyzer/providers",
		"DELETE /api/ai/session-analyzer/rules/:name",
		"DELETE /api/connections/:nameOrID",
		"DELETE /api/datamasking-rules/:id",
		"DELETE /api/guardrails/:id",
		"DELETE /api/users/:emailOrID",
		"GET /api/access-requests/rules",
		"GET /api/access-requests/rules/:name",
		"GET /api/ai/session-analyzer/providers",
		"GET /api/ai/session-analyzer/rules",
		"GET /api/ai/session-analyzer/rules/:name",
		"GET /api/ai/session-analyzer/system-prompt",
		"GET /api/callback",
		"GET /api/connection-tags",
		"GET /api/connections",
		"GET /api/connections/:nameOrID",
		"GET /api/connections/:nameOrID/ai-session-analyzer-rule",
		"GET /api/datamasking-rules",
		"GET /api/datamasking-rules/:id",
		"GET /api/feature-flags",
		"GET /api/guardrails",
		"GET /api/guardrails/:id",
		"GET /api/healthz",
		"GET /api/login",
		"GET /api/openapiv2.json",
		"GET /api/openapiv3.json",
		"GET /api/plugins",
		"GET /api/plugins/:name",
		"GET /api/publicserverinfo",
		"GET /api/reviews",
		"GET /api/reviews/:id",
		"GET /api/saml/login",
		"GET /api/serverinfo",
		"GET /api/sessions",
		"GET /api/sessions/:session_id",
		"GET /api/userinfo",
		"GET /api/users",
		"GET /api/users/:emailOrID",
		"GET /api/users/groups",
		"HEAD /api/access-requests/rules",
		"HEAD /api/access-requests/rules/:name",
		"HEAD /api/ai/session-analyzer/providers",
		"HEAD /api/ai/session-analyzer/rules",
		"HEAD /api/ai/session-analyzer/rules/:name",
		"HEAD /api/ai/session-analyzer/system-prompt",
		"HEAD /api/callback",
		"HEAD /api/connection-tags",
		"HEAD /api/connections",
		"HEAD /api/connections/:nameOrID",
		"HEAD /api/connections/:nameOrID/ai-session-analyzer-rule",
		"HEAD /api/datamasking-rules",
		"HEAD /api/datamasking-rules/:id",
		"HEAD /api/feature-flags",
		"HEAD /api/guardrails",
		"HEAD /api/guardrails/:id",
		"HEAD /api/healthz",
		"HEAD /api/login",
		"HEAD /api/openapiv2.json",
		"HEAD /api/openapiv3.json",
		"HEAD /api/plugins",
		"HEAD /api/plugins/:name",
		"HEAD /api/publicserverinfo",
		"HEAD /api/reviews",
		"HEAD /api/reviews/:id",
		"HEAD /api/saml/login",
		"HEAD /api/serverinfo",
		"HEAD /api/sessions",
		"HEAD /api/sessions/:session_id",
		"HEAD /api/userinfo",
		"HEAD /api/users",
		"HEAD /api/users/:emailOrID",
		"HEAD /api/users/groups",
		"PATCH /api/connections/:nameOrID",
		"PATCH /api/sessions/:session_id/metadata",
		"PATCH /api/users/self/slack",
		"POST /api/access-requests/rules",
		"POST /api/ai/session-analyzer/providers",
		"POST /api/ai/session-analyzer/rules",
		"POST /api/connections",
		"POST /api/datamasking-rules",
		"POST /api/guardrails",
		"POST /api/localauth/login",
		"POST /api/localauth/register",
		"POST /api/orgs/invitations",
		"POST /api/plugins",
		"POST /api/saml/callback",
		"POST /api/signup",
		"POST /api/users",
		"POST /api/users/self/signup-origin",
		"PUT /api/access-requests/rules/:name",
		"PUT /api/ai/session-analyzer/rules/:name",
		"PUT /api/datamasking-rules/:id",
		"PUT /api/feature-flags/:name",
		"PUT /api/guardrails/:id",
		"PUT /api/plugins/:name",
		"PUT /api/plugins/:name/config",
		"PUT /api/users/:emailOrID",
	}
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
