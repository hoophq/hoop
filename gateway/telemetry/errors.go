package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/go-logr/logr"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// exportError is a failed export reduced to a fixed vocabulary: the signal
// and a class. The exporters' own errors carry the request URL, whose path
// can hold a vendor key, and the collector's answer body, which can echo
// one. Every exporter here returns this instead, so no error that leaves
// the package says more than the class.
type exportError struct {
	signal, class string
}

func (e *exportError) Error() string { return e.signal + " export failed: " + e.class }

// statusRecorder keeps the status code of the last response, so a failed
// export can name it. An exporter sends one request at a time, so the last
// status is the failed request's.
type statusRecorder struct {
	next http.RoundTripper
	last atomic.Int32
}

// RoundTrip clears the status first, so a retry that gets no answer (a
// refused connection, a timeout) does not report an earlier attempt's.
func (r *statusRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.last.Store(0)
	resp, err := r.next.RoundTrip(req)
	if err == nil {
		r.last.Store(int32(resp.StatusCode))
	}
	return resp, err
}

func (r *statusRecorder) reset() {
	if r != nil {
		r.last.Store(0)
	}
}

func (r *statusRecorder) status() int {
	if r == nil {
		return 0
	}
	return int(r.last.Load())
}

// classify names what went wrong, in a fixed set: an HTTP status the
// collector answered, a timeout, a connection failure, a gRPC status code,
// or "export error" for the rest.
func classify(err error, httpStatus int) string {
	if httpStatus >= 300 {
		return fmt.Sprintf("http %d %s", httpStatus, http.StatusText(httpStatus))
	}
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return "timeout"
	}
	if s, ok := status.FromError(err); ok && s.Code() != codes.Unknown && s.Code() != codes.OK {
		switch s.Code() {
		case codes.Unavailable:
			return "connection"
		case codes.DeadlineExceeded:
			return "timeout"
		default:
			return "grpc " + s.Code().String()
		}
	}
	var opErr *net.OpError
	var dnsErr *net.DNSError
	if errors.As(err, &opErr) || errors.As(err, &dnsErr) || errors.Is(err, syscall.ECONNREFUSED) {
		return "connection"
	}
	return "export error"
}

// describe is what may be logged about err: every exportError it holds,
// and nothing of any other error's text.
func describe(err error) string {
	var found []string
	seen := map[string]bool{}
	var walk func(error)
	walk = func(err error) {
		if err == nil {
			return
		}
		var ee *exportError
		if e, ok := err.(*exportError); ok {
			ee = e
		}
		if ee != nil {
			if s := ee.Error(); !seen[s] {
				seen[s] = true
				found = append(found, s)
			}
			return
		}
		switch u := err.(type) {
		case interface{ Unwrap() []error }:
			for _, e := range u.Unwrap() {
				walk(e)
			}
		case interface{ Unwrap() error }:
			walk(u.Unwrap())
		}
	}
	walk(err)
	if len(found) == 0 {
		return "an OpenTelemetry error outside an export (text withheld)"
	}
	return strings.Join(found, "; ")
}

// metricExporter returns an exportError for every failure.
type metricExporter struct {
	sdkmetric.Exporter
	rec *statusRecorder
}

func (m metricExporter) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	m.rec.reset()
	if err := m.Exporter.Export(ctx, rm); err != nil {
		return &exportError{signal: "metrics", class: classify(err, m.rec.status())}
	}
	return nil
}

func (m metricExporter) ForceFlush(ctx context.Context) error {
	if err := m.Exporter.ForceFlush(ctx); err != nil {
		return &exportError{signal: "metrics", class: classify(err, 0)}
	}
	return nil
}

func (m metricExporter) Shutdown(ctx context.Context) error {
	if err := m.Exporter.Shutdown(ctx); err != nil {
		return &exportError{signal: "metrics", class: classify(err, 0)}
	}
	return nil
}

// spanExporter returns an exportError for every failure.
type spanExporter struct {
	sdktrace.SpanExporter
	rec *statusRecorder
}

func (s spanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	s.rec.reset()
	if err := s.SpanExporter.ExportSpans(ctx, spans); err != nil {
		return &exportError{signal: "traces", class: classify(err, s.rec.status())}
	}
	return nil
}

func (s spanExporter) Shutdown(ctx context.Context) error {
	if err := s.SpanExporter.Shutdown(ctx); err != nil {
		return &exportError{signal: "traces", class: classify(err, 0)}
	}
	return nil
}

// sdkLogger is the OpenTelemetry SDK's internal logger. It discards: the SDK
// logs whole URLs and answers there. A test swaps it to look.
var sdkLogger = logr.Discard()
