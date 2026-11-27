package telemetry

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	colmetricpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/grpc"
	_ "google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/stats"
	"google.golang.org/protobuf/proto"
)

// cloudWatchAddonEnv is the OTEL_* set the EKS CloudWatch Observability
// add-on leaves on a gateway container: Auto monitor, all four languages,
// Application Signals on (amazon-cloudwatch-agent-operator 5bc85bef). The
// $(...) references are expanded, as the kubelet does.
var cloudWatchAddonEnv = map[string]string{
	"OTEL_EXPORTER_OTLP_ENDPOINT":                    "http://cloudwatch-agent.amazon-cloudwatch:4316",
	"OTEL_EXPORTER_OTLP_PROTOCOL":                    "http/protobuf",
	"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT":             "http://cloudwatch-agent.amazon-cloudwatch:4316/v1/traces",
	"OTEL_EXPORTER_OTLP_TRACES_PROTOCOL":             "http/protobuf",
	"OTEL_EXPORTER_OTLP_METRICS_PROTOCOL":            "http/protobuf",
	"OTEL_TRACES_SAMPLER":                            "xray",
	"OTEL_TRACES_SAMPLER_ARG":                        "endpoint=http://cloudwatch-agent.amazon-cloudwatch:2000",
	"OTEL_TRACES_EXPORTER":                           "otlp",
	"OTEL_METRICS_EXPORTER":                          "none",
	"OTEL_LOGS_EXPORTER":                             "none",
	"OTEL_PROPAGATORS":                               "tracecontext,baggage,xray",
	"OTEL_SERVICE_NAME":                              "hoopgateway",
	"OTEL_RESOURCE_ATTRIBUTES_NODE_NAME":             "ip-10-0-1-23.ec2.internal",
	"OTEL_AWS_APP_SIGNALS_ENABLED":                   "true",
	"OTEL_AWS_APPLICATION_SIGNALS_ENABLED":           "true",
	"OTEL_AWS_APPLICATION_SIGNALS_RUNTIME_ENABLED":   "true",
	"OTEL_AWS_APP_SIGNALS_EXPORTER_ENDPOINT":         "http://cloudwatch-agent.amazon-cloudwatch:4316/v1/metrics",
	"OTEL_AWS_APPLICATION_SIGNALS_EXPORTER_ENDPOINT": "http://cloudwatch-agent.amazon-cloudwatch:4316/v1/metrics",
	"OTEL_AWS_OTLP_METRICS_ENDPOINT":                 "http://cloudwatch-agent.amazon-cloudwatch:4316/v1/metrics",
	"OTEL_AWS_OTLP_LOGS_ENDPOINT":                    "http://cloudwatch-agent.amazon-cloudwatch:4316/v1/logs",
	"OTEL_AWS_DYNAMIC_INSTRUMENTATION_API_URL":       "http://cloudwatch-agent.amazon-cloudwatch:2000",
	"OTEL_PYTHON_DISTRO":                             "aws_distro",
	"OTEL_PYTHON_CONFIGURATOR":                       "aws_configurator",
	"OTEL_DOTNET_DISTRO":                             "aws_distro",
	"OTEL_DOTNET_CONFIGURATOR":                       "aws_configurator",
	"OTEL_DOTNET_AUTO_HOME":                          "/otel-auto-instrumentation-dotnet",
	"OTEL_DOTNET_AUTO_PLUGINS":                       "AWS.Distro.OpenTelemetry.AutoInstrumentation.Plugin, AWS.Distro.OpenTelemetry.AutoInstrumentation",
	"OTEL_RESOURCE_ATTRIBUTES": "com.amazonaws.cloudwatch.entity.internal.service.name.source=K8sWorkload," +
		"k8s.container.name=hoopgateway,k8s.namespace.name=hoopdev,k8s.node.name=ip-10-0-1-23.ec2.internal," +
		"k8s.pod.name=hoopgateway-7d9c5b7f4-x2x9q,service.instance.id=injected-instance,service.version=1.328.0",
}

// sdkEnv sets the other OTEL_* variables the SDK reads by itself, each to a
// value that would change what the export sends if it were read. The
// OTEL_GO_X_* ones have no option that overrides them.
var sdkEnv = map[string]string{
	"OTEL_GO_X_METRIC_EXPORT_BATCH_SIZE":                       "1",
	"OTEL_GO_X_PER_SERIES_START_TIMESTAMPS":                    "true",
	"OTEL_GO_X_CARDINALITY_LIMIT":                              "1",
	"OTEL_GO_X_RESOURCE":                                       "true",
	"OTEL_GO_X_OBSERVABILITY":                                  "true",
	"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT":                      "http://cloudwatch-agent.amazon-cloudwatch:4316/v1/metrics",
	"OTEL_EXPORTER_OTLP_INSECURE":                              "false",
	"OTEL_EXPORTER_OTLP_CERTIFICATE":                           "/nonexistent/ca.pem",
	"OTEL_EXPORTER_OTLP_HEADERS":                               "x-injected=1",
	"OTEL_EXPORTER_OTLP_COMPRESSION":                           "gzip",
	"OTEL_EXPORTER_OTLP_TIMEOUT":                               "1",
	"OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE":        "delta",
	"OTEL_EXPORTER_OTLP_METRICS_DEFAULT_HISTOGRAM_AGGREGATION": "base2_exponential_bucket_histogram",
	"OTEL_METRIC_EXPORT_INTERVAL":                              "1",
	"OTEL_METRIC_EXPORT_TIMEOUT":                               "1",
	"OTEL_METRICS_EXEMPLAR_FILTER":                             "always_on",
	"OTEL_ATTRIBUTE_COUNT_LIMIT":                               "0",
	"OTEL_SPAN_ATTRIBUTE_COUNT_LIMIT":                          "0",
	"OTEL_BSP_MAX_QUEUE_SIZE":                                  "0",
	"OTEL_BSP_SCHEDULE_DELAY":                                  "1",
}

const (
	injectedChildEnv = "HOOP_TELEMETRY_INJECTED_CHILD"
	childDropped     = "CHILD: dropped %d OTEL_* variables\n"
	childExportOff   = "CHILD: export off"
	childExported    = "CHILD: exported"
)

// TestInjectedOTELChild is the gateway the test below starts, in the order
// gateway.Run boots: DropOTELEnv, appconfig.Load, then, with export on,
// telemetry.Start, one sidecar handshake, one API request and a flush. It
// does nothing in a normal run.
func TestInjectedOTELChild(t *testing.T) {
	if os.Getenv(injectedChildEnv) == "" {
		t.Skip("run as a child of TestAnInjectedOTELVariableChangesNothing only")
	}
	dropped, err := DropOTELEnv()
	require.NoError(t, err)
	fmt.Printf(childDropped, len(dropped))
	require.NoError(t, appconfig.Load())
	cfg := appconfig.Get().OTLPExport()
	if cfg == nil {
		fmt.Println(childExportOff)
		return
	}
	require.NoError(t, Start(cfg, "test", nil))
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(HTTPServer())
	none := func(*gin.Context) (string, string) { return "", "" }
	r.POST("/api/sidecars/handshake", SidecarHandshake(none), func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/api/x", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/sidecars/handshake", nil))
	// Two series of http.server.request.duration, recorded at two times:
	// per-series start timestamps would tell them apart.
	time.Sleep(10 * time.Millisecond)
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/x", nil))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, Shutdown(ctx))
	fmt.Println(childExported)
}

// A platform injects the standard OTEL_* variables into every pod, and the
// SDK reads some of them by itself with no option to override them
// (OTEL_GO_X_*). gateway.Run removes them all first. Then an injected value
// neither stops startup, nor turns the export on, nor changes what the
// export sends.
//
// This is also the check for an SDK upgrade: package init runs before
// main, so an SDK that read OTEL_* at init would see them before
// DropOTELEnv. The injected run would then differ from the clean one.
func TestAnInjectedOTELVariableChangesNothing(t *testing.T) {
	t.Run("the add-on's set alone: startup succeeds, export off", func(t *testing.T) {
		out := runInjectedChild(t, cloudWatchAddonEnv)
		assert.True(t, strings.Contains(out, fmt.Sprintf(childDropped, len(cloudWatchAddonEnv))), "the child dropped another count")
		assert.True(t, strings.Contains(out, childExportOff), "the child did not report export off")
	})

	t.Run("the standard names alone turn nothing on", func(t *testing.T) {
		rcv := newHTTPReceiver(t)
		out := runInjectedChild(t, map[string]string{
			"OTEL_EXPORTER_OTLP_ENDPOINT": "http://" + rcv.addr,
			"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf",
			"OTEL_TRACES_SAMPLER":         "always_on",
		})
		assert.True(t, strings.Contains(out, childExportOff), "the child did not report export off")
		assert.Empty(t, rcv.exports())
	})

	injected := map[string]string{}
	for k, v := range cloudWatchAddonEnv {
		injected[k] = v
	}
	for k, v := range sdkEnv {
		injected[k] = v
	}
	for _, protocol := range []string{appconfig.OTLPProtocolHTTP, appconfig.OTLPProtocolGRPC} {
		t.Run(protocol+": the export is the same with and without the injected set", func(t *testing.T) {
			clean := exportWith(t, protocol, nil)
			// What the HOOP_OTEL_* set below asks for.
			assert.Equal(t, 1, clean.MetricsRequests, "one metrics export, the flush: the interval is an hour")
			assert.Equal(t, 1, clean.TracesRequests, "one traces export, the flush")
			assert.Equal(t, "hoop-gateway-test", clean.Resource["service.name"])
			assert.Equal(t, "hoop-test", clean.Resource["deployment.environment"])
			assert.Equal(t, []string{"1"}, clean.HoopHeader)

			assert.Equal(t, clean, exportWith(t, protocol, injected))
		})
	}
}

// exportShape is what a run sent, without what differs between two runs
// of the same process: times, ids, ports and values.
type exportShape struct {
	MetricsRequests, TracesRequests int
	Targets, Encodings              []string
	HoopHeader, InjectedHeader      []string
	// Resource is without service.instance.id, random per process.
	Resource map[string]string
	// Metrics maps a metric name to its type, temporality, series,
	// distinct start timestamps and exemplars.
	Metrics map[string]string
	// Spans are each span's name and attribute keys.
	Spans []string
}

// exportWith runs the child with a HOOP_OTEL_* set pointing at a local
// receiver, plus injected, and returns what the receiver got.
func exportWith(t *testing.T, protocol string, injected map[string]string) exportShape {
	t.Helper()
	var rcv *receiver
	var endpoint string
	if protocol == appconfig.OTLPProtocolGRPC {
		rcv = newGRPCReceiver(t)
		endpoint = "http://" + rcv.addr
	} else {
		rcv = newHTTPReceiver(t)
		endpoint = "http://" + rcv.addr + "/hoop"
	}
	env := map[string]string{
		"HOOP_OTEL_EXPORTER_OTLP_ENDPOINT": endpoint,
		"HOOP_OTEL_EXPORTER_OTLP_PROTOCOL": protocol,
		"HOOP_OTEL_EXPORTER_OTLP_HEADERS":  "x-hoop-test=1",
		"HOOP_OTEL_EXPORTER_OTLP_TIMEOUT":  "5000",
		"HOOP_OTEL_METRIC_EXPORT_INTERVAL": "3600000",
		"HOOP_OTEL_TRACES_SAMPLER":         "always_on",
		"HOOP_OTEL_SERVICE_NAME":           "hoop-gateway-test",
		"HOOP_OTEL_RESOURCE_ATTRIBUTES":    "deployment.environment=hoop-test",
	}
	for k, v := range injected {
		env[k] = v
	}
	out := runInjectedChild(t, env)
	require.True(t, strings.Contains(out, fmt.Sprintf(childDropped, len(injected))), "the child dropped another count")
	require.True(t, strings.Contains(out, childExported), "the child did not report the export")
	assert.False(t, strings.Contains(out, "telemetry: "), "the child logged a telemetry warning")
	return shapeOf(rcv.exports())
}

func shapeOf(got []received) exportShape {
	s := exportShape{Resource: map[string]string{}, Metrics: map[string]string{}}
	targets, encodings, hoop, injected := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	type series struct {
		kind, temporality string
		points, exemplars int
		starts            map[uint64]bool
	}
	metrics := map[string]*series{}
	for _, r := range got {
		targets[r.target] = true
		encodings[r.encoding] = true
		hoop[r.header("x-hoop-test")] = true
		injected[r.header("x-injected")] = true
		if r.metrics != nil {
			s.MetricsRequests++
			for _, rm := range r.metrics.ResourceMetrics {
				for k, v := range attrMap(rm.Resource.GetAttributes()) {
					s.Resource[k] = v
				}
				for _, sm := range rm.ScopeMetrics {
					for _, m := range sm.Metrics {
						x := metrics[m.Name]
						if x == nil {
							x = &series{starts: map[uint64]bool{}}
							metrics[m.Name] = x
						}
						switch d := m.Data.(type) {
						case *metricpb.Metric_Sum:
							x.kind, x.temporality = "sum", d.Sum.AggregationTemporality.String()
							for _, p := range d.Sum.DataPoints {
								x.points++
								x.exemplars += len(p.Exemplars)
								x.starts[p.StartTimeUnixNano] = true
							}
						case *metricpb.Metric_Histogram:
							x.kind, x.temporality = "histogram", d.Histogram.AggregationTemporality.String()
							for _, p := range d.Histogram.DataPoints {
								x.points++
								x.exemplars += len(p.Exemplars)
								x.starts[p.StartTimeUnixNano] = true
							}
						case *metricpb.Metric_ExponentialHistogram:
							x.kind, x.temporality = "exponential histogram", d.ExponentialHistogram.AggregationTemporality.String()
							for _, p := range d.ExponentialHistogram.DataPoints {
								x.points++
								x.exemplars += len(p.Exemplars)
								x.starts[p.StartTimeUnixNano] = true
							}
						default:
							x.kind = fmt.Sprintf("%T", d)
						}
					}
				}
			}
		}
		if r.traces != nil {
			s.TracesRequests++
			for _, rs := range r.traces.ResourceSpans {
				for k, v := range attrMap(rs.Resource.GetAttributes()) {
					s.Resource[k] = v
				}
				for _, ss := range rs.ScopeSpans {
					for _, sp := range ss.Spans {
						var keys []string
						for k := range attrMap(sp.Attributes) {
							keys = append(keys, k)
						}
						sort.Strings(keys)
						s.Spans = append(s.Spans, sp.Name+": "+strings.Join(keys, ","))
					}
				}
			}
		}
	}
	delete(s.Resource, "service.instance.id")
	for name, x := range metrics {
		s.Metrics[name] = fmt.Sprintf("%s %s series=%d starts=%d exemplars=%d",
			x.kind, x.temporality, x.points, len(x.starts), x.exemplars)
	}
	sort.Strings(s.Spans)
	s.Targets, s.Encodings, s.HoopHeader, s.InjectedHeader = keys(targets), keys(encodings), keys(hoop), keys(injected)
	return s
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// runInjectedChild runs TestInjectedOTELChild in an environment built from
// scratch: PATH, HOME, TMPDIR and env. Its output is not copied into a
// failure message: it can hold an endpoint.
func runInjectedChild(t *testing.T, env map[string]string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestInjectedOTELChild$", "-test.count=1", "-test.v")
	for _, k := range []string{"PATH", "HOME", "TMPDIR"} {
		if v, ok := os.LookupEnv(k); ok {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	cmd.Env = append(cmd.Env, injectedChildEnv+"=1", "LOG_ENCODING=json")
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	require.NoError(t, ctx.Err(), "the child hung")
	if err != nil {
		t.Fatalf("the child failed (%v); run TestInjectedOTELChild with %s=1 to see its output", err, injectedChildEnv)
	}
	return out.String()
}

// received is one export request, decoded.
type received struct {
	signal, target, protocol, encoding string
	headers                            map[string][]string
	metrics                            *colmetricpb.ExportMetricsServiceRequest
	traces                             *coltracepb.ExportTraceServiceRequest
}

func (r received) header(name string) string {
	for k, v := range r.headers {
		if strings.EqualFold(k, name) && len(v) > 0 {
			return v[0]
		}
	}
	return ""
}

type receiver struct {
	addr string
	mu   sync.Mutex
	got  []received
}

func (r *receiver) add(x received) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, x)
}

func (r *receiver) exports() []received {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]received(nil), r.got...)
}

// readBody is an export request's body, gunzipped when it says gzip.
func readBody(r *http.Request) ([]byte, error) {
	var body io.Reader = r.Body
	if r.Header.Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		body = zr
	}
	return io.ReadAll(body)
}

func newHTTPReceiver(t *testing.T) *receiver {
	rcv := &receiver{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		x := received{target: r.URL.Path, protocol: appconfig.OTLPProtocolHTTP,
			encoding: r.Header.Get("Content-Encoding"), headers: r.Header}
		body, err := readBody(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/v1/metrics"):
			x.signal, x.metrics = "metrics", &colmetricpb.ExportMetricsServiceRequest{}
			err = proto.Unmarshal(body, x.metrics)
		case strings.HasSuffix(r.URL.Path, "/v1/traces"):
			x.signal, x.traces = "traces", &coltracepb.ExportTraceServiceRequest{}
			err = proto.Unmarshal(body, x.traces)
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		rcv.add(x)
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	t.Cleanup(srv.Close)
	rcv.addr = srv.Listener.Addr().String()
	return rcv
}

// grpcEncoding carries the compression of one RPC from the stats handler,
// which sees the request headers, to the service handler.
type grpcEncoding struct{ name string }

type grpcEncodingKey struct{}

type grpcEncodingTap struct{}

func (grpcEncodingTap) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context {
	return context.WithValue(ctx, grpcEncodingKey{}, &grpcEncoding{})
}

func (grpcEncodingTap) HandleRPC(ctx context.Context, s stats.RPCStats) {
	if in, ok := s.(*stats.InHeader); ok {
		if e, ok := ctx.Value(grpcEncodingKey{}).(*grpcEncoding); ok {
			e.name = in.Compression
		}
	}
}

func (grpcEncodingTap) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context { return ctx }
func (grpcEncodingTap) HandleConn(context.Context, stats.ConnStats)                       {}

func grpcReceived(ctx context.Context, signal string) received {
	method, _ := grpc.Method(ctx)
	x := received{signal: signal, target: method, protocol: appconfig.OTLPProtocolGRPC}
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		x.headers = md
	}
	if e, ok := ctx.Value(grpcEncodingKey{}).(*grpcEncoding); ok {
		x.encoding = e.name
	}
	return x
}

type grpcMetrics struct {
	colmetricpb.UnimplementedMetricsServiceServer
	rcv *receiver
}

func (s grpcMetrics) Export(ctx context.Context, req *colmetricpb.ExportMetricsServiceRequest) (*colmetricpb.ExportMetricsServiceResponse, error) {
	x := grpcReceived(ctx, "metrics")
	x.metrics = req
	s.rcv.add(x)
	return &colmetricpb.ExportMetricsServiceResponse{}, nil
}

type grpcTraces struct {
	coltracepb.UnimplementedTraceServiceServer
	rcv *receiver
}

func (s grpcTraces) Export(ctx context.Context, req *coltracepb.ExportTraceServiceRequest) (*coltracepb.ExportTraceServiceResponse, error) {
	x := grpcReceived(ctx, "traces")
	x.traces = req
	s.rcv.add(x)
	return &coltracepb.ExportTraceServiceResponse{}, nil
}

func newGRPCReceiver(t *testing.T) *receiver {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	rcv := &receiver{addr: ln.Addr().String()}
	srv := grpc.NewServer(grpc.StatsHandler(grpcEncodingTap{}))
	colmetricpb.RegisterMetricsServiceServer(srv, grpcMetrics{rcv: rcv})
	coltracepb.RegisterTraceServiceServer(srv, grpcTraces{rcv: rcv})
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	t.Cleanup(func() {
		srv.Stop()
		if err := <-served; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			t.Errorf("grpc receiver: %v", err)
		}
	})
	return rcv
}

func attrMap(kvs []*commonpb.KeyValue) map[string]string {
	m := map[string]string{}
	for _, kv := range kvs {
		m[kv.Key] = kv.Value.GetStringValue()
	}
	return m
}
