package telemetry

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// The SDK exporters read the OTEL_* variables on their own. With the
// environment saying the opposite of appconfig, the first bytes on the wire
// must still be what appconfig decided: a TLS ClientHello for a secure
// export, cleartext HTTP/1 or HTTP/2 for an insecure one.
func TestTheExportKeepsAppconfigsTransportOverTheEnvironment(t *testing.T) {
	gauge := &metricdata.ResourceMetrics{ScopeMetrics: []metricdata.ScopeMetrics{{Metrics: []metricdata.Metrics{{
		Name: "x", Data: metricdata.Gauge[int64]{DataPoints: []metricdata.DataPoint[int64]{{Value: 1}}},
	}}}}}
	signals := map[string]func(ctx context.Context, cfg *appconfig.OTLPExport) error{
		"metrics": func(ctx context.Context, cfg *appconfig.OTLPExport) error {
			exp, err := newMetricExporter(ctx, cfg)
			if err != nil {
				return err
			}
			defer func() { _ = exp.Shutdown(context.Background()) }()
			return exp.Export(ctx, gauge)
		},
		"traces": func(ctx context.Context, cfg *appconfig.OTLPExport) error {
			exp, err := newTraceExporter(ctx, cfg)
			if err != nil {
				return err
			}
			defer func() { _ = exp.Shutdown(context.Background()) }()
			return exp.ExportSpans(ctx, tracetest.SpanStubs{{Name: "x"}}.Snapshots())
		},
	}
	for _, protocol := range []string{appconfig.OTLPProtocolGRPC, appconfig.OTLPProtocolHTTP} {
		for _, insecure := range []bool{false, true} {
			for signal, export := range signals {
				name := protocol + "/" + signal + "/secure"
				if insecure {
					name = protocol + "/" + signal + "/insecure"
				}
				t.Run(name, func(t *testing.T) {
					ln, err := net.Listen("tcp", "127.0.0.1:0")
					require.NoError(t, err)
					t.Cleanup(func() { _ = ln.Close() })
					addr := ln.Addr().String()

					// The environment contradicts the config on every knob
					// the SDK reads for the transport.
					envScheme, envInsecure := "http://", "true"
					if insecure {
						envScheme, envInsecure = "https://", "false"
					}
					for _, k := range []string{"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"} {
						t.Setenv(k, envScheme+addr)
					}
					for _, k := range []string{"OTEL_EXPORTER_OTLP_INSECURE", "OTEL_EXPORTER_OTLP_METRICS_INSECURE", "OTEL_EXPORTER_OTLP_TRACES_INSECURE"} {
						t.Setenv(k, envInsecure)
					}

					first := make(chan []byte, 1)
					go func() {
						conn, err := ln.Accept()
						if err != nil {
							first <- nil
							return
						}
						defer conn.Close()
						_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
						buf := make([]byte, 5)
						n, _ := io.ReadFull(conn, buf)
						first <- buf[:n]
					}()

					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					defer cancel()
					cfg := &appconfig.OTLPExport{Protocol: protocol, Host: addr, Insecure: insecure,
						Timeout: time.Second, Interval: time.Hour}
					go func() { _ = export(ctx, cfg) }()

					var got []byte
					select {
					case got = <-first:
					case <-time.After(5 * time.Second):
						t.Fatal("the exporter never connected")
					}
					require.NotEmpty(t, got)
					switch {
					case !insecure:
						assert.Equal(t, byte(0x16), got[0], "want a TLS handshake, got %q", got)
					case protocol == appconfig.OTLPProtocolGRPC:
						assert.Equal(t, "PRI *", string(got), "want the cleartext HTTP/2 preface")
					default:
						assert.Equal(t, "POST ", string(got), "want a cleartext HTTP/1 request")
					}
				})
			}
		}
	}
}

// A handler that panics is recorded as the 500 the recovery writes, and
// its span ends.
func TestAPanicInTheHandlerIsRecordedAsAnError(t *testing.T) {
	for _, tc := range []struct {
		name   string
		path   string
		hook   func(SidecarOf) gin.HandlerFunc
		metric string
		want   string
	}{
		{"handshake", "/api/sidecars/handshake", SidecarHandshake, "hoop.gateway.sidecar.handshakes", HandshakeError},
		{"events", "/api/sidecars/events", SidecarEvents, "hoop.gateway.session_events.batches", BatchError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader, spans := startTest(t, nil, true)
			gin.SetMode(gin.TestMode)
			r := gin.New()
			r.Use(gin.RecoveryWithWriter(io.Discard))
			none := func(*gin.Context) (string, string) { return "", "" }
			r.POST(tc.path, tc.hook(none), func(*gin.Context) { panic("boom") })

			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader("{}")))
			assert.Equal(t, http.StatusInternalServerError, rec.Code)

			assert.Equal(t, map[string]float64{tc.want: 1}, byOutcome(collect(t, reader, tc.metric)))
			ended := spans.Ended()
			require.Len(t, ended, 1, "the span ends")
			assert.Equal(t, codes.Error, ended[0].Status().Code)
		})
	}
}
