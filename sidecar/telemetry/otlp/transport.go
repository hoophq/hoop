package otlp

import (
	"context"
	"net/http"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// propagator is the W3C trace context, also installed as the global in New.
// Named here so Inject and HTTPTransport work the same before New runs
// and in a binary where another library swapped the global.
var propagator = propagation.TraceContext{}

// Inject writes traceparent and tracestate for the span in ctx into h.
// Nothing is written when ctx carries no valid span context, so a caller
// outside a trace (traces off, exchange not sampled) pays a context lookup
// and no header.
func (*Exporter) Inject(ctx context.Context, h http.Header) {
	inject(ctx, h)
}

func inject(ctx context.Context, h http.Header) {
	if !trace.SpanContextFromContext(ctx).IsValid() {
		return
	}
	propagator.Inject(ctx, propagation.HeaderCarrier(h))
}

// HTTPTransport wraps base so every request carries the trace context of
// its own context. Hand-rolled rather than otelhttp: that package starts a
// client span per request and reads the global tracer provider, neither of
// which this module wants (package doc), and the whole job is one header.
// A nil base is http.DefaultTransport.
func HTTPTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return propagatingTransport{base: base}
}

type propagatingTransport struct {
	base http.RoundTripper
}

// RoundTrip injects into a clone: a RoundTripper must not modify the
// request it is given (net/http contract), and the caller may reuse it.
func (t propagatingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	if !trace.SpanContextFromContext(ctx).IsValid() {
		return t.base.RoundTrip(req)
	}
	req = req.Clone(ctx)
	inject(ctx, req.Header)
	return t.base.RoundTrip(req)
}
