package telemetry

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// startTest installs an exporter over an in-memory reader and, with traces,
// a span recorder. Nothing leaves the process.
func startTest(t *testing.T, db *sql.DB, traces bool) (*sdkmetric.ManualReader, *tracetest.SpanRecorder) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	var rec *tracetest.SpanRecorder
	var spans sdktrace.SpanProcessor
	if traces {
		rec = tracetest.NewSpanRecorder()
		spans = rec
	}
	e, err := newExporter(buildResource(&appconfig.OTLPExport{}, "test"), reader, spans, sdktrace.AlwaysSample(), db)
	require.NoError(t, err)
	current.Store(e)
	t.Cleanup(func() { require.NoError(t, Shutdown(context.Background())) })
	return reader, rec
}

type point struct {
	attrs map[string]string
	value float64
}

// collect returns the points of one metric: the value of a sum or gauge,
// the count of a histogram.
func collect(t *testing.T, reader *sdkmetric.ManualReader, name string) []point {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	attrs := func(s attribute.Set) map[string]string {
		m := map[string]string{}
		for _, kv := range s.ToSlice() {
			m[string(kv.Key)] = kv.Value.Emit()
		}
		return m
	}
	var out []point
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			switch d := m.Data.(type) {
			case metricdata.Sum[int64]:
				for _, dp := range d.DataPoints {
					out = append(out, point{attrs(dp.Attributes), float64(dp.Value)})
				}
			case metricdata.Sum[float64]:
				for _, dp := range d.DataPoints {
					out = append(out, point{attrs(dp.Attributes), dp.Value})
				}
			case metricdata.Gauge[int64]:
				for _, dp := range d.DataPoints {
					out = append(out, point{attrs(dp.Attributes), float64(dp.Value)})
				}
			case metricdata.Histogram[float64]:
				for _, dp := range d.DataPoints {
					out = append(out, point{attrs(dp.Attributes), float64(dp.Count)})
				}
			}
		}
	}
	return out
}

// byOutcome sums points per hoop.outcome.
func byOutcome(points []point) map[string]float64 {
	out := map[string]float64{}
	for _, p := range points {
		out[p.attrs[attrOutcome]] += p.value
	}
	return out
}

// sidecarRoute registers path the way the API does: the telemetry hook,
// then an auth stand-in that refuses a request without a token, then a
// handler that answers the status the request asks for.
func sidecarRoute(path string, hook func(SidecarOf) gin.HandlerFunc, handler gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	auth := func(c *gin.Context) {
		if c.GetHeader("token") == "" {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Set("sidecar", c.GetHeader("token"))
	}
	of := func(c *gin.Context) (string, string) {
		if id := c.GetString("sidecar"); id != "" {
			return id, "org-1"
		}
		return "", ""
	}
	r.POST(path, hook(of), auth, handler)
	return r
}

func answerStatus(c *gin.Context) {
	status, _ := strconv.Atoi(c.GetHeader("answer"))
	c.Status(status)
}

func post(r http.Handler, path, token, answer string, header ...string) {
	req := httptest.NewRequest(http.MethodPost, path, nil)
	if token != "" {
		req.Header.Set("token", token)
	}
	req.Header.Set("answer", answer)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	r.ServeHTTP(httptest.NewRecorder(), req)
}

func TestHandshakesCountByOutcome(t *testing.T) {
	reader, spans := startTest(t, nil, true)
	r := sidecarRoute("/api/sidecars/handshake", SidecarHandshake, answerStatus)

	const parent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	post(r, "/api/sidecars/handshake", "sc-1", "200", "traceparent", parent)
	post(r, "/api/sidecars/handshake", "sc-1", "200")
	post(r, "/api/sidecars/handshake", "", "200")
	post(r, "/api/sidecars/handshake", "sc-1", "403")
	post(r, "/api/sidecars/handshake", "sc-1", "412")
	post(r, "/api/sidecars/handshake", "sc-1", "422")
	post(r, "/api/sidecars/handshake", "sc-1", "500")

	want := map[string]float64{HandshakeOK: 2, HandshakeRejected: 2, HandshakeNoConfig: 1, HandshakeInvalid: 1, HandshakeError: 1}
	assert.Equal(t, want, byOutcome(collect(t, reader, "hoop.gateway.sidecar.handshakes")))
	assert.Equal(t, want, byOutcome(collect(t, reader, "hoop.gateway.sidecar.handshake.duration")))

	ended := spans.Ended()
	require.Len(t, ended, 7)
	first := ended[0]
	assert.Equal(t, "sidecar handshake", first.Name())
	assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", first.SpanContext().TraceID().String(), "the span joins the caller's trace")
	spanAttrs := map[string]string{}
	for _, kv := range first.Attributes() {
		spanAttrs[string(kv.Key)] = kv.Value.Emit()
	}
	assert.Equal(t, map[string]string{
		attrOutcome: HandshakeOK, attrStatusCode: "200", attrSidecarID: "sc-1", attrOrgID: "org-1",
	}, spanAttrs)
	for _, kv := range ended[2].Attributes() {
		assert.NotEqual(t, attrSidecarID, string(kv.Key), "a refused credential names no sidecar")
	}
	assert.Equal(t, codes.Unset, ended[3].Status().Code, "a 4xx is an answer, not a failure")
	assert.Equal(t, codes.Error, ended[6].Status().Code)
}

func TestSessionEventsCountBatchesAndEvents(t *testing.T) {
	reader, spans := startTest(t, nil, true)
	r := sidecarRoute("/api/sidecars/events", SidecarEvents, func(c *gin.Context) {
		if c.GetHeader("answer") == "200" || c.GetHeader("answer") == "422" {
			SetEventCount(c, 3)
		}
		answerStatus(c)
	})

	post(r, "/api/sidecars/events", "sc-1", "200")
	post(r, "/api/sidecars/events", "sc-1", "200")
	post(r, "/api/sidecars/events", "sc-1", "503")
	post(r, "/api/sidecars/events", "sc-1", "413")
	post(r, "/api/sidecars/events", "sc-1", "422")
	post(r, "/api/sidecars/events", "", "200")
	post(r, "/api/sidecars/events", "sc-1", "500")

	assert.Equal(t, map[string]float64{BatchAccepted: 2, BatchShed: 1, BatchRejected: 3, BatchError: 1},
		byOutcome(collect(t, reader, "hoop.gateway.session_events.batches")))
	assert.Equal(t, map[string]float64{BatchAccepted: 2, BatchShed: 1, BatchRejected: 3, BatchError: 1},
		byOutcome(collect(t, reader, "hoop.gateway.session_events.batch.duration")))
	assert.Equal(t, map[string]float64{BatchAccepted: 6, BatchRejected: 3},
		byOutcome(collect(t, reader, "hoop.gateway.session_events.events")),
		"events count only in batches whose body was read")

	ended := spans.Ended()
	require.Len(t, ended, 7)
	assert.Equal(t, "sidecar session events", ended[0].Name())
	var count string
	for _, kv := range ended[0].Attributes() {
		if string(kv.Key) == attrEventCount {
			count = kv.Value.Emit()
		}
	}
	assert.Equal(t, "3", count)
}

func TestHTTPServerRecordsTheRouteTemplate(t *testing.T) {
	reader, _ := startTest(t, nil, false)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(HTTPServer())
	r.Handle(http.MethodGet, "/api/things/:id", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.Handle("PROPFIND", "/api/things/:id", func(c *gin.Context) { c.Status(http.StatusOK) })

	for _, path := range []string{"/api/things/1", "/api/things/2", "/api/nothing"} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("PROPFIND", "/api/things/3", nil))

	got := map[string]float64{}
	for _, p := range collect(t, reader, "http.server.request.duration") {
		got[p.attrs[attrMethod]+" "+p.attrs[attrRoute]+" "+p.attrs[attrStatusCode]] = p.value
	}
	assert.Equal(t, map[string]float64{
		"GET /api/things/:id 200":    2,
		"GET  404":                   1,
		"_OTHER /api/things/:id 200": 1,
	}, got)
}

// A pool on a driver that needs no server: the gauges read sql.DBStats.
func TestDBPoolGauges(t *testing.T) {
	sql.Register("telemetry-fake", fakeDriver{})
	db, err := sql.Open("telemetry-fake", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(7)
	held, err := db.Conn(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = held.Close() })

	reader, _ := startTest(t, db, false)
	for name, want := range map[string]float64{
		"hoop.gateway.db.connections.open":          1,
		"hoop.gateway.db.connections.in_use":        1,
		"hoop.gateway.db.connections.idle":          0,
		"hoop.gateway.db.connections.max":           7,
		"hoop.gateway.db.connections.waits":         0,
		"hoop.gateway.db.connections.wait_duration": 0,
	} {
		points := collect(t, reader, name)
		require.Len(t, points, 1, name)
		assert.Equal(t, want, points[0].value, name)
	}
}

// Export off: every hook hands the request on and records nothing.
func TestHooksPassThroughWhenOff(t *testing.T) {
	require.NoError(t, Shutdown(context.Background()))
	ran := 0
	r := sidecarRoute("/api/sidecars/events", SidecarEvents, func(c *gin.Context) {
		ran++
		SetEventCount(c, 1)
		c.Status(http.StatusOK)
	})
	post(r, "/api/sidecars/events", "sc-1", "200")
	assert.Equal(t, 1, ran)

	r = gin.New()
	r.Use(HTTPServer())
	r.GET("/x", func(c *gin.Context) { ran++ })
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	assert.Equal(t, 2, ran)
}

func TestResource(t *testing.T) {
	res := buildResource(&appconfig.OTLPExport{}, "1.2.3")
	attrs := map[string]string{}
	for _, kv := range res.Attributes() {
		attrs[string(kv.Key)] = kv.Value.Emit()
	}
	assert.Equal(t, ServiceName, attrs["service.name"])
	assert.Equal(t, "1.2.3", attrs["service.version"])
	assert.Equal(t, SchemaVersion, attrs["hoop.schema.version"])
	assert.NotEmpty(t, attrs["service.instance.id"])
	assert.Equal(t, SchemaURL, res.SchemaURL())

	res = buildResource(&appconfig.OTLPExport{
		ServiceName: "hoop-control-plane",
		Resource:    map[string]string{"service.name": "ignored", "deployment.environment": "prod"},
	}, "1.2.3")
	attrs = map[string]string{}
	for _, kv := range res.Attributes() {
		attrs[string(kv.Key)] = kv.Value.Emit()
	}
	assert.Equal(t, "hoop-control-plane", attrs["service.name"], "OTEL_SERVICE_NAME wins")
	assert.Equal(t, "prod", attrs["deployment.environment"])
}

func TestSampler(t *testing.T) {
	for name, want := range map[string]string{
		"always_on":                "AlwaysOnSampler",
		"always_off":               "AlwaysOffSampler",
		"traceidratio":             "TraceIDRatioBased{0.25}",
		"parentbased_always_on":    "ParentBased{root:AlwaysOnSampler",
		"parentbased_always_off":   "ParentBased{root:AlwaysOffSampler",
		"parentbased_traceidratio": "ParentBased{root:TraceIDRatioBased{0.25}",
	} {
		got := sampler(&appconfig.OTLPExport{Sampler: name, SamplerArg: 0.25}).Description()
		assert.Contains(t, got, want, name)
	}
}

type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) { return fakeConn{}, nil }

type fakeConn struct{}

func (fakeConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("not supported") }
func (fakeConn) Close() error                        { return nil }
func (fakeConn) Begin() (driver.Tx, error)           { return nil, errors.New("not supported") }
