package api

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	apiprovisioning "github.com/hoophq/hoop/gateway/api/provisioning"
	apisidecar "github.com/hoophq/hoop/gateway/api/sidecar"
	"github.com/hoophq/hoop/gateway/appconfig"
)

func routeSet(engine *gin.Engine) map[string]bool {
	set := map[string]bool{}
	for _, r := range engine.Routes() {
		set[r.Method+" "+r.Path] = true
	}
	return set
}

// The control plane serves exactly the routes the gateway serves, the web UI
// included; only the /healthz handler differs. A route added to the gateway
// reaches the control plane by construction, and this test fails when either
// surface drifts from the other (ADR-0013).
func TestControlPlaneServesEveryGatewayRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	gateway := routeSet((&Api{}).buildEngine(appconfig.AppModeGateway))
	controlPlane := routeSet((&Api{}).buildEngine(appconfig.AppModeControlPlane))
	if len(gateway) == 0 {
		t.Fatal("the gateway registered no routes")
	}

	for route := range gateway {
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

// Every route whose data only a control plane has answers 412 on a gateway
// (ADR-0020), so the gateway behaves as it did before those routes existed.
// The table is checked against the registered routes: a route that moves to
// another handler fails here instead of silently losing its guard.
func TestControlPlaneOnlyRoutesAnswer412OnAGateway(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if appconfig.Get().IsControlPlane() {
		t.Fatal("this test binary must run as a gateway")
	}
	registered := map[string]string{}
	for _, r := range (&Api{}).buildEngine(appconfig.AppModeGateway).Routes() {
		registered[r.Method+" "+r.Path] = r.Handler
	}

	apiPrefix := "/api"
	for _, tt := range []struct {
		route   string
		handler gin.HandlerFunc
	}{
		{"GET /serverconfig/directory-sync", apiprovisioning.GetDirectorySync},
		{"PUT /serverconfig/directory-sync", apiprovisioning.PutDirectorySync},
		{"DELETE /serverconfig/directory-sync", apiprovisioning.DeleteDirectorySync},
		{"POST /serverconfig/directory-sync/run", apiprovisioning.RunDirectorySync},
		{"GET /serverconfig/directory-sync/groups", apiprovisioning.ListDirectorySyncGroups},
		{"GET /sidecars/:nameOrID/slack-channels", apisidecar.GetSlackChannels},
		{"PUT /sidecars/:nameOrID/slack-channels", apisidecar.PutSlackChannels},
	} {
		t.Run(tt.route, func(t *testing.T) {
			method, path, _ := strings.Cut(tt.route, " ")
			got, ok := registered[method+" "+apiPrefix+path]
			if !ok {
				t.Fatalf("route %s %s is not registered", method, apiPrefix+path)
			}
			if want := runtime.FuncForPC(reflect.ValueOf(tt.handler).Pointer()).Name(); got != want {
				t.Fatalf("route handler = %s, want %s", got, want)
			}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(method, apiPrefix+path, nil)
			tt.handler(c)
			if w.Code != http.StatusPreconditionFailed {
				t.Errorf("status = %d, want 412: %s", w.Code, w.Body.String())
			}
		})
	}
}
