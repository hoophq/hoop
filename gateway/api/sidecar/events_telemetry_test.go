package apisidecar

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/telemetry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"
)

const eventsShedTelemetryOrgID = "00000000-0000-0000-0000-0000000000f7"

// otlpReceiver is an OTLP/HTTP collector that keeps every metrics request.
type otlpReceiver struct {
	mu   sync.Mutex
	reqs []*colmetricspb.ExportMetricsServiceRequest
}

func (r *otlpReceiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	var m colmetricspb.ExportMetricsServiceRequest
	if req.URL.Path != "/v1/metrics" || proto.Unmarshal(body, &m) != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	r.mu.Lock()
	r.reqs = append(r.reqs, &m)
	r.mu.Unlock()
	w.Header().Set("Content-Type", "application/x-protobuf")
	_, _ = w.Write(nil)
}

// sums adds up every int sum point of name, by hoop.outcome.
func (r *otlpReceiver) sums(name string) map[string]int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]int64{}
	for _, req := range r.reqs {
		for _, rm := range req.ResourceMetrics {
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					if m.Name != name || m.GetSum() == nil {
						continue
					}
					for _, dp := range m.GetSum().DataPoints {
						for _, kv := range dp.Attributes {
							if kv.Key == "hoop.outcome" {
								out[kv.Value.GetStringValue()] += dp.GetAsInt()
							}
						}
					}
				}
			}
		}
	}
	return out
}

// The ingest's real 503, a full replica refusing a batch before it reads the
// body, reaches the collector as shed, with no events counted.
func TestATelemetryHookCountsTheIngest503AsShed(t *testing.T) {
	enableSessionEvents(t, eventsShedTelemetryOrgID)
	services.SizeSidecarEventSlots(5)
	t.Cleanup(func() { services.SizeSidecarEventSlots(50) })
	wait := eventsAdmitWait
	eventsAdmitWait = 0
	t.Cleanup(func() { eventsAdmitWait = wait })
	release, ok := services.AdmitSidecarEvents(context.Background(), 0)
	require.True(t, ok)
	t.Cleanup(release)

	rcv := &otlpReceiver{}
	collector := httptest.NewServer(rcv)
	t.Cleanup(collector.Close)
	require.NoError(t, telemetry.Start(&appconfig.OTLPExport{
		Protocol: appconfig.OTLPProtocolHTTP, Host: collector.Listener.Addr().String(), Insecure: true,
		Timeout: 5 * time.Second, Interval: time.Hour,
	}, "test", nil))
	t.Cleanup(func() { _ = telemetry.Shutdown(context.Background()) })

	gin.SetMode(gin.TestMode)
	r := gin.New()
	sc := memorySidecar(eventsShedTelemetryOrgID)
	none := func(*gin.Context) (string, string) { return "", "" }
	r.POST("/api/sidecars/events", telemetry.SidecarEvents(none), func(c *gin.Context) {
		c.Set("sidecar-auth", sc)
		PostEvents(c)
	})
	body := &readSpy{r: strings.NewReader(`{"events":[{"session_id":"s1","seq":1}]}`)}
	req := httptest.NewRequest(http.MethodPost, "/api/sidecars/events", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	assert.False(t, body.read, "the body was read before the 503")

	// Shutdown flushes the last collection to the collector.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, telemetry.Shutdown(ctx))

	assert.Equal(t, map[string]int64{telemetry.BatchShed: 1}, rcv.sums("hoop.gateway.session_events.batches"))
	assert.Empty(t, rcv.sums("hoop.gateway.session_events.events"), "a shed batch counts no events")
}
