package otlp

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	collectorlogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

// receiver is an OTLP/http collector in a test: it decodes the protobuf
// POSTed to the three signal paths and keeps what it saw. Nothing here
// talks to the network beyond the loopback server.
type receiver struct {
	srv *httptest.Server

	mu      sync.Mutex
	metrics []*metricspb.ResourceMetrics
	logs    []*logspb.ResourceLogs
	traces  []*tracepb.ResourceSpans
	// headers of the last request to /echo, for the propagation test.
	echoed http.Header
}

func newReceiver(t *testing.T) *receiver {
	t.Helper()
	r := &receiver{}
	r.srv = httptest.NewServer(http.HandlerFunc(r.serve))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *receiver) serve(w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	switch req.URL.Path {
	case "/v1/metrics":
		var m collectormetrics.ExportMetricsServiceRequest
		if err := proto.Unmarshal(body, &m); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		r.metrics = append(r.metrics, m.ResourceMetrics...)
	case "/v1/logs":
		var m collectorlogs.ExportLogsServiceRequest
		if err := proto.Unmarshal(body, &m); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		r.logs = append(r.logs, m.ResourceLogs...)
	case "/v1/traces":
		var m collectortrace.ExportTraceServiceRequest
		if err := proto.Unmarshal(body, &m); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		r.traces = append(r.traces, m.ResourceSpans...)
	case "/echo":
		r.echoed = req.Header.Clone()
	default:
		http.NotFound(w, req)
		return
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
}

// metric returns every data point of the named metric, flattened across
// exports, with its attributes as a map.
func (r *receiver) metric(name string) []point {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []point
	for _, rm := range r.metrics {
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				if m.Name != name {
					continue
				}
				switch d := m.Data.(type) {
				case *metricspb.Metric_Sum:
					for _, dp := range d.Sum.DataPoints {
						out = append(out, point{attrs: attrMap(dp.Attributes), value: float64(dp.GetAsInt()) + dp.GetAsDouble()})
					}
				case *metricspb.Metric_Gauge:
					for _, dp := range d.Gauge.DataPoints {
						out = append(out, point{attrs: attrMap(dp.Attributes), value: float64(dp.GetAsInt()) + dp.GetAsDouble()})
					}
				case *metricspb.Metric_Histogram:
					for _, dp := range d.Histogram.DataPoints {
						out = append(out, point{attrs: attrMap(dp.Attributes), value: float64(dp.Count)})
					}
				}
			}
		}
	}
	return out
}

// attributeKeys returns every attribute key on every metric data point.
func (r *receiver) metricAttributeKeys() map[string]bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	keys := map[string]bool{}
	add := func(kvs []*commonpb.KeyValue) {
		for _, kv := range kvs {
			keys[kv.Key] = true
		}
	}
	for _, rm := range r.metrics {
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				switch d := m.Data.(type) {
				case *metricspb.Metric_Sum:
					for _, dp := range d.Sum.DataPoints {
						add(dp.Attributes)
					}
				case *metricspb.Metric_Gauge:
					for _, dp := range d.Gauge.DataPoints {
						add(dp.Attributes)
					}
				case *metricspb.Metric_Histogram:
					for _, dp := range d.Histogram.DataPoints {
						add(dp.Attributes)
					}
				}
			}
		}
	}
	return keys
}

func (r *receiver) resourceAttrs() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]string{}
	for _, rm := range r.metrics {
		for k, v := range attrMap(rm.Resource.GetAttributes()) {
			out[k] = v
		}
	}
	return out
}

func (r *receiver) logRecords() []*logspb.LogRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*logspb.LogRecord
	for _, rl := range r.logs {
		for _, sl := range rl.ScopeLogs {
			out = append(out, sl.LogRecords...)
		}
	}
	return out
}

func (r *receiver) spans() []*tracepb.Span {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*tracepb.Span
	for _, rs := range r.traces {
		for _, ss := range rs.ScopeSpans {
			out = append(out, ss.Spans...)
		}
	}
	return out
}

func (r *receiver) echoedHeader(name string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.echoed.Get(name)
}

type point struct {
	attrs map[string]string
	value float64
}

// attrMap flattens OTLP attributes to strings: ints and string slices are
// rendered so a test can compare with one map lookup.
func attrMap(kvs []*commonpb.KeyValue) map[string]string {
	m := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		m[kv.Key] = anyString(kv.Value)
	}
	return m
}

func anyString(v *commonpb.AnyValue) string {
	switch x := v.Value.(type) {
	case *commonpb.AnyValue_StringValue:
		return x.StringValue
	case *commonpb.AnyValue_IntValue:
		return strconv.FormatInt(x.IntValue, 10)
	case *commonpb.AnyValue_BoolValue:
		if x.BoolValue {
			return "true"
		}
		return "false"
	case *commonpb.AnyValue_ArrayValue:
		s := ""
		for i, e := range x.ArrayValue.Values {
			if i > 0 {
				s += ","
			}
			s += anyString(e)
		}
		return s
	default:
		return v.String()
	}
}
