package telemetry

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// Attribute keys. A dashboard is written against these spellings; renaming
// one is a SchemaVersion major bump. The http.* keys are the OpenTelemetry
// semantic conventions.
const (
	attrOutcome    = "hoop.outcome"
	attrSidecarID  = "hoop.sidecar.id"
	attrOrgID      = "hoop.org.id"
	attrEventCount = "hoop.session_events.count"
	attrRoute      = "http.route"
	attrMethod     = "http.request.method"
	attrStatusCode = "http.response.status_code"
)

// Handshake outcomes, from the status the handshake answered.
const (
	HandshakeOK       = "ok"
	HandshakeRejected = "rejected"
	HandshakeNoConfig = "no_config"
	HandshakeInvalid  = "invalid"
	HandshakeError    = "error"
)

// Session event batch outcomes, from the status the ingest answered.
const (
	BatchAccepted = "accepted"
	BatchShed     = "shed"
	BatchRejected = "rejected"
	BatchError    = "error"
)

// eventCountKey holds the batch size the events handler read, for the
// middleware that counts it after the handler returns.
const eventCountKey = "hoop.telemetry.event-count"

// propagator reads a caller's W3C trace context. Not the global one: no
// global is set (package doc).
var propagator = propagation.TraceContext{}

// SidecarOf names the sidecar a request authenticated as, empty before the
// auth middleware ran or when it refused. It is passed in so this package
// does not import the API's routing.
type SidecarOf func(c *gin.Context) (sidecarID, orgID string)

// HTTPServer records http.server.request.duration for every request, by
// route template, method and status. The template, not the path, so a path
// parameter mints no series; a request no route matched has no http.route.
// It goes first in the chain, so the status it reads is the one a panic
// recovery wrote.
func HTTPServer() gin.HandlerFunc {
	return func(c *gin.Context) {
		e := current.Load()
		if e == nil {
			c.Next()
			return
		}
		start := time.Now()
		c.Next()
		attrs := make([]attribute.KeyValue, 0, 3)
		attrs = append(attrs,
			attribute.String(attrMethod, method(c.Request.Method)),
			attribute.Int(attrStatusCode, c.Writer.Status()))
		if route := c.FullPath(); route != "" {
			attrs = append(attrs, attribute.String(attrRoute, route))
		}
		e.httpDuration.Record(context.Background(), time.Since(start).Seconds(),
			metric.WithAttributeSet(attribute.NewSet(attrs...)))
	}
}

// SidecarHandshake counts each handshake by outcome, times it and opens a
// span around it. Registered before the sidecar auth middleware, so a
// refused credential counts too.
func SidecarHandshake(sidecarOf SidecarOf) gin.HandlerFunc {
	return func(c *gin.Context) {
		e := current.Load()
		if e == nil {
			c.Next()
			return
		}
		start := time.Now()
		span := e.startSpan(c, "sidecar handshake")
		defer finish(c, func(status int) {
			outcome := handshakeOutcome(status)
			ctx := c.Request.Context()
			set := metric.WithAttributeSet(attribute.NewSet(attribute.String(attrOutcome, outcome)))
			e.handshakes.Add(ctx, 1, set)
			e.handshakeDuration.Record(ctx, time.Since(start).Seconds(), set)
			endSpan(c, span, sidecarOf, outcome, status)
		})
		c.Next()
	}
}

// SidecarEvents counts each session event batch and its events by outcome,
// times it and opens a span around it. Registered before the sidecar auth
// middleware, like SidecarHandshake. The handler reports the batch size
// with SetEventCount once it has read the body.
func SidecarEvents(sidecarOf SidecarOf) gin.HandlerFunc {
	return func(c *gin.Context) {
		e := current.Load()
		if e == nil {
			c.Next()
			return
		}
		start := time.Now()
		span := e.startSpan(c, "sidecar session events")
		defer finish(c, func(status int) {
			outcome := batchOutcome(status)
			ctx := c.Request.Context()
			set := metric.WithAttributeSet(attribute.NewSet(attribute.String(attrOutcome, outcome)))
			e.batches.Add(ctx, 1, set)
			e.batchDuration.Record(ctx, time.Since(start).Seconds(), set)
			n := c.GetInt(eventCountKey)
			if n > 0 {
				e.events.Add(ctx, int64(n), set)
			}
			if span != nil {
				span.SetAttributes(attribute.Int(attrEventCount, n))
			}
			endSpan(c, span, sidecarOf, outcome, status)
		})
		c.Next()
	}
}

// finish runs record with the request's status once the handlers return,
// and also when one panics: the recovery middleware sits outside these
// hooks and writes its 500 only after they unwind, so a panic is recorded
// as that 500 here, then handed on to it.
func finish(c *gin.Context, record func(status int)) {
	if r := recover(); r != nil {
		record(http.StatusInternalServerError)
		panic(r)
	}
	record(c.Writer.Status())
}

// SetEventCount reports how many events the batch carries. A batch refused
// before its body was read reports none.
func SetEventCount(c *gin.Context, n int) {
	c.Set(eventCountKey, n)
}

// handshakeOutcome maps the handshake's status onto its fixed outcomes.
func handshakeOutcome(status int) string {
	switch {
	case status < 300:
		return HandshakeOK
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return HandshakeRejected
	case status == http.StatusPreconditionFailed:
		return HandshakeNoConfig
	case status < 500:
		return HandshakeInvalid
	default:
		return HandshakeError
	}
}

// batchOutcome maps the ingest's status onto its fixed outcomes. A 503 is
// the ingest shedding load: the sidecar keeps the batch and resends it.
// Every other 4xx tells the sidecar not to resend.
func batchOutcome(status int) string {
	switch {
	case status < 300:
		return BatchAccepted
	case status == http.StatusServiceUnavailable:
		return BatchShed
	case status < 500:
		return BatchRejected
	default:
		return BatchError
	}
}

// startSpan opens a server span parented on the caller's trace context and
// hands it to the handlers through the request context. Nil when traces are
// off.
func (e *exporter) startSpan(c *gin.Context, name string) trace.Span {
	if e.tracer == nil {
		return nil
	}
	ctx := propagator.Extract(c.Request.Context(), propagation.HeaderCarrier(c.Request.Header))
	ctx, span := e.tracer.Start(ctx, name, trace.WithSpanKind(trace.SpanKindServer))
	c.Request = c.Request.WithContext(ctx)
	return span
}

// endSpan closes span with the outcome and who called. A 5xx is an error;
// a 4xx is the gateway answering as designed.
func endSpan(c *gin.Context, span trace.Span, sidecarOf SidecarOf, outcome string, status int) {
	if span == nil {
		return
	}
	span.SetAttributes(attribute.String(attrOutcome, outcome), attribute.Int(attrStatusCode, status))
	if id, org := sidecarOf(c); id != "" {
		span.SetAttributes(attribute.String(attrSidecarID, id), attribute.String(attrOrgID, org))
	}
	if status >= 500 {
		span.SetStatus(codes.Error, strconv.Itoa(status)+" "+http.StatusText(status))
	}
	span.End()
}

// method bounds http.request.method to the methods HTTP defines, as the
// semantic conventions say: anything else is _OTHER.
func method(m string) string {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace:
		return m
	default:
		return "_OTHER"
	}
}
