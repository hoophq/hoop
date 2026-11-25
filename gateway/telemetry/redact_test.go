package telemetry

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-logr/logr/funcr"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// A vendor key in the endpoint path, or echoed by the collector, reaches
// neither the gateway log nor the SDK's internal logger nor the error
// Shutdown returns. What is logged is the signal and the failure class.
func TestExportFailuresLogOnlyTheirClass(t *testing.T) {
	const secret = "s3cr3t-vendor-key"
	rejecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("invalid key " + secret))
	}))
	t.Cleanup(rejecting.Close)
	gone := httptest.NewServer(http.NotFoundHandler())
	goneAddr := gone.Listener.Addr().String()
	gone.Close()

	for _, tc := range []struct {
		name, host, path, class string
	}{
		{"401 with the key in the body", rejecting.Listener.Addr().String(), "/" + secret, "http 401 Unauthorized"},
		{"escaped percent in the path", rejecting.Listener.Addr().String(), "/%" + secret, "http 401 Unauthorized"},
		{"collector gone", goneAddr, "/" + secret, "connection"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var out []string
			capture := func(s string) {
				mu.Lock()
				defer mu.Unlock()
				out = append(out, s)
			}
			logged := func() string {
				mu.Lock()
				defer mu.Unlock()
				return strings.Join(out, "\n")
			}
			prevWarn, prevSDK := warnf, sdkLogger
			warnf = func(format string, args ...any) { capture(fmt.Sprintf(format, args...)) }
			sdkLogger = funcr.New(func(prefix, args string) { capture("sdk: " + prefix + " " + args) }, funcr.Options{Verbosity: 10})
			t.Cleanup(func() { warnf, sdkLogger = prevWarn, prevSDK })

			require.NoError(t, Start(&appconfig.OTLPExport{
				Protocol: appconfig.OTLPProtocolHTTP, Host: tc.host, Path: tc.path, Insecure: true,
				Timeout: 300 * time.Millisecond, Interval: 100 * time.Millisecond,
				Sampler: "always_on", SamplerArg: 1,
			}, "test", nil))
			t.Cleanup(func() { _ = Shutdown(context.Background()) })

			gin.SetMode(gin.TestMode)
			r := gin.New()
			r.Use(HTTPServer())
			none := func(*gin.Context) (string, string) { return "", "" }
			r.POST("/api/sidecars/handshake", SidecarHandshake(none), func(c *gin.Context) { c.Status(http.StatusOK) })
			r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/sidecars/handshake", nil))

			want := "metrics export failed: " + tc.class
			require.Eventually(t, func() bool { return strings.Contains(logged(), want) }, 5*time.Second, 50*time.Millisecond,
				"want %q in the log, got %q", want, logged())
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			err := Shutdown(ctx)
			// The span batch fails at the flush and reaches the handler.
			assert.Contains(t, logged(), "traces export failed: "+tc.class)

			all := logged()
			if err != nil {
				all += "\n" + err.Error()
			}
			assert.NotContains(t, all, secret)
			// The host may appear (the start line names it); a request URL
			// may not.
			assert.NotContains(t, all, "/v1/metrics", "no request URL is logged")
			assert.NotContains(t, all, "/v1/traces", "no request URL is logged")
		})
	}
}

func TestDescribeWithholdsOtherErrors(t *testing.T) {
	assert.Equal(t, "an OpenTelemetry error outside an export (text withheld)",
		describe(fmt.Errorf("parse %q: bad", "http://c/s3cr3t")))
	joined := fmt.Errorf("metrics: %w", &exportError{signal: "metrics", class: "timeout"})
	assert.Equal(t, "metrics export failed: timeout", describe(joined))
}

// A collector that never answers: each request is cut at
// OTEL_EXPORTER_OTLP_TIMEOUT (200ms here), for metrics and traces, and the
// failure is a timeout. The collector reads the body and holds the answer;
// it measures how long until the client gives up on the request. The export
// runs under a 3s deadline, as the periodic reader and the batch processor
// run it, so a client without the timeout holds the request for 3s.
func TestAStalledRequestIsCutAtTheTimeout(t *testing.T) {
	release := make(chan struct{})
	held := make(chan time.Duration, 16)
	stalled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The request context ends on a client disconnect only once the
		// body is read.
		_, _ = io.Copy(io.Discard, r.Body)
		start := time.Now()
		select {
		case <-release:
		case <-r.Context().Done():
		}
		held <- time.Since(start)
	}))
	t.Cleanup(stalled.Close)
	t.Cleanup(func() { close(release) })
	cfg := &appconfig.OTLPExport{Protocol: appconfig.OTLPProtocolHTTP, Host: stalled.Listener.Addr().String(),
		Insecure: true, Timeout: 200 * time.Millisecond, Interval: time.Hour}

	exports := map[string]func(context.Context) error{
		"metrics": func(ctx context.Context) error {
			exp, err := newMetricExporter(context.Background(), cfg)
			require.NoError(t, err)
			return exp.Export(ctx, gaugeForTest())
		},
		"traces": func(ctx context.Context) error {
			exp, err := newTraceExporter(context.Background(), cfg)
			require.NoError(t, err)
			return exp.ExportSpans(ctx, spansForTest())
		},
	}
	for signal, export := range exports {
		t.Run(signal, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			err := export(ctx)
			require.Error(t, err)
			assert.Equal(t, signal+" export failed: timeout", err.Error())
			select {
			case d := <-held:
				assert.Less(t, d, time.Second, "the stalled request was held %s, past the 200ms timeout", d)
			case <-time.After(5 * time.Second):
				t.Fatal("the client never gave up on the request")
			}
			for len(held) > 0 {
				<-held
			}
		})
	}
}

// A retry that gets no answer reports no status, not the earlier attempt's.
func TestTheStatusRecorderForgetsAnEarlierAnswer(t *testing.T) {
	calls := 0
	rec := &statusRecorder{next: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: http.NoBody}, nil
		}
		return nil, &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}
	})}
	req := httptest.NewRequest(http.MethodPost, "http://collector/v1/metrics", nil)
	resp, err := rec.RoundTrip(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusServiceUnavailable, rec.status())

	_, err = rec.RoundTrip(req)
	require.Error(t, err)
	assert.Equal(t, 0, rec.status())
	assert.Equal(t, "connection", classify(err, rec.status()))
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func gaugeForTest() *metricdata.ResourceMetrics {
	return &metricdata.ResourceMetrics{ScopeMetrics: []metricdata.ScopeMetrics{{Metrics: []metricdata.Metrics{{
		Name: "x", Data: metricdata.Gauge[int64]{DataPoints: []metricdata.DataPoint[int64]{{Value: 1}}},
	}}}}}
}

func spansForTest() []sdktrace.ReadOnlySpan { return tracetest.SpanStubs{{Name: "x"}}.Snapshots() }
