package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/apiroutes"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/telemetry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The telemetry hooks sit before the sidecar auth middleware. Turning the
// export on must not change what a refused credential gets back.
func TestSidecarAuthAnswersAreTheSameWithTelemetryOn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a := &Api{}
	route := a.newEngine()
	a.buildSidecarRoutes(a.newAPIRouter(route, ""))

	answers := func() []string {
		var out []string
		for _, path := range []string{"/api/sidecars/handshake", "/api/sidecars/events"} {
			for _, headers := range []map[string]string{
				{},
				{apiroutes.SidecarTokenHeader: "hsc_x", apiroutes.SidecarIdentityHeader: "jwt"},
			} {
				req := httptest.NewRequest(http.MethodPost, path, nil)
				for k, v := range headers {
					req.Header.Set(k, v)
				}
				rec := httptest.NewRecorder()
				route.ServeHTTP(rec, req)
				out = append(out, path+" "+http.StatusText(rec.Code)+" "+rec.Body.String())
			}
		}
		return out
	}

	off := answers()
	require.NoError(t, telemetry.Start(&appconfig.OTLPExport{
		Protocol: appconfig.OTLPProtocolGRPC, Host: "127.0.0.1:1", Insecure: true,
		Timeout: 100 * time.Millisecond, Interval: time.Hour,
	}, "test", nil))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = telemetry.Shutdown(ctx) // the collector is not there; only the stop matters
	})
	on := answers()

	assert.Equal(t, off, on)
	assert.Contains(t, off[0], "Unauthorized")
	assert.Contains(t, off[1], "Bad Request")
}
