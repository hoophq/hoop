package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/appconfig"
)

func routeSet(engine *gin.Engine) map[string]bool {
	set := map[string]bool{}
	for _, r := range engine.Routes() {
		set[r.Method+" "+r.Path] = true
	}
	return set
}

// The control plane serves every route the gateway serves; only the web UI
// stays out, because the control plane has its own frontend. A route added
// to the gateway reaches the control plane by construction, and this test
// fails when either surface drifts from the other (ADR-0013).
func TestControlPlaneServesEveryGatewayRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	gateway := routeSet((&Api{}).buildEngine(appconfig.AppModeGateway))
	controlPlane := routeSet((&Api{}).buildEngine(appconfig.AppModeControlPlane))
	if len(gateway) == 0 {
		t.Fatal("the gateway registered no routes")
	}

	// Registered only when a web UI build resolves, so they may be absent
	// from the gateway set too; they must never be in the control plane's.
	baseURL := appconfig.Get().ApiURLPath()
	webUI := map[string]bool{
		"GET " + baseURL + "/index.html": true,
		"GET " + baseURL + "/js/app.js":  true,
	}

	for route := range gateway {
		if webUI[route] {
			if controlPlane[route] {
				t.Errorf("control plane serves the web UI route %s", route)
			}
			continue
		}
		if !controlPlane[route] {
			t.Errorf("gateway route %s is missing from the control plane", route)
		}
	}
	for route := range controlPlane {
		if !gateway[route] {
			t.Errorf("control plane route %s is missing from the gateway", route)
		}
	}
}

// Liveness must not depend on the gRPC transport: the control plane never
// starts one, so probing it would fail every health check.
func TestControlPlaneHealthzIsOKWithoutGRPC(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := (&Api{}).buildEngine(appconfig.AppModeControlPlane)

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/healthz", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d (body=%s)", w.Code, http.StatusOK, w.Body.String())
	}
}
