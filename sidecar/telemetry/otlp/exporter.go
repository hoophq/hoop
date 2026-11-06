// Package otlp is the OpenTelemetry exporter behind sidecar/telemetry: it
// turns the hooks the data path calls into OTLP metrics, spans and log
// records and pushes them to one collector.
//
// # Why a nested module
//
// The root module carries libhoop and nothing else (sidecar/CLAUDE.md). The
// OpenTelemetry SDK, its three OTLP exporters, gRPC and protobuf are a
// dependency tree of their own, so they live here, behind the interfaces
// telemetry declares. A binary that wants the exporter imports this module
// and hands telemetry.Builder to the daemon; one that does not links none
// of it and refuses a "telemetry" block.
//
// # Why (almost) no globals
//
// New never calls otel.SetMeterProvider, SetTracerProvider or the logger
// equivalent. The sidecar is also a library (hoop start sidecar embeds it),
// and a global provider would be inherited by every other instrumented
// library in the host binary: their spans would ride our exporter, with our
// resource, to our collector. The providers stay on the Exporter. Two
// globals are set: the W3C propagator, because a propagator is process-wide
// by nature and inheriting it is the point, and the SDK error handler, which
// the OTLP exporters report through (otel.Handle) and which has no
// per-provider form; it is rate-limited so a dead collector cannot flood the
// host's log, and it costs any other SDK user in the binary nothing but the
// same rate limit.
//
// # Cost on the data path
//
// Every LaneRecorder hook costs one instrument Record or Add with an
// attribute set built once and cached by its handful of enum values, so a
// statement allocates no map and no slice. With traces off the Begin* hooks
// return the caller's context and a closure that only counts; nothing in
// the SDK runs. The audit sink never blocks and never fails: it is a copy
// of a trail the file sink already holds (audit package doc), so a slow
// collector costs dropped records, counted, and nothing else.
package otlp

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/credentials"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/telemetry"
)

// scopeName names the meter, tracer and logger. One name for the three so a
// backend groups every signal of the sidecar under one instrumentation
// scope.
const scopeName = "github.com/hoophq/hoop/sidecar"

// Attribute keys, schema v1. A dashboard is written against these spellings;
// renaming one is a telemetry.SchemaVersion major bump.
const (
	attrLane       = "hoop.lane"
	attrProtocol   = "hoop.protocol"
	attrDirection  = "hoop.direction"
	attrOperation  = "hoop.operation"
	attrVerdict    = "hoop.verdict"
	attrDenySource = "hoop.deny.source"
	attrRule       = "hoop.rule"
	attrRiskLevel  = "hoop.risk_level"
	attrStage      = "hoop.stage"
	attrEngine     = "hoop.engine"
	attrPairing    = "hoop.pairing"
	attrOutcome    = "hoop.outcome"
	attrAnalyzer   = "hoop.analyzer"
	attrProvider   = "hoop.provider"
	attrModel      = "hoop.model"
	attrCallKind   = "hoop.analyzer.kind"
	attrStatus     = "hoop.status"
	attrAction     = "hoop.action"

	// Spans and log records only. Never on a metric: principal and session
	// id are unbounded, and a metric series per user is a cardinality bomb.
	attrPrincipal      = "hoop.principal"
	attrSessionID      = "hoop.session_id"
	attrTables         = "hoop.tables"
	attrDatabase       = "hoop.database"
	attrHTTPMethod     = "hoop.http.method"
	attrHTTPPath       = "hoop.http.path"
	attrHTTPStatusCode = "hoop.http.status_code"
	attrHTTPHost       = "hoop.http.host"
	attrHTTPResource   = "hoop.http.resource"
	attrHTTPQuery      = "hoop.http.query"
	attrHTTPTarget     = "hoop.http.target"
	attrHTTPBody       = "hoop.http.body"
	attrMaskedEntities = "hoop.masked.entities"
	attrMaskedCount    = "hoop.masked.count"
	attrReviewID       = "hoop.review.id"
	attrMessage        = "hoop.message"
	attrStatement      = "hoop.statement"
	attrError          = "hoop.error"
	attrDurationMS     = "hoop.duration_ms"
	attrStatementCount = "hoop.statement_count"
	attrDeniedCount    = "hoop.denied_count"
	attrMetaPrefix     = "hoop.meta."
)

// stageBuckets are the statement.duration histogram bounds, in seconds. The
// top end goes to ten minutes because a review hold runs for minutes and a
// histogram whose last bucket is 10s would file every approval under +Inf.
var stageBuckets = []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 300, 600}

// analyzerBuckets fit a model provider's round trip: seconds, not
// milliseconds, and nothing finer than a quarter second matters.
var analyzerBuckets = []float64{0.25, 0.5, 1, 2, 3, 5, 7.5, 10, 15, 20, 30, 45, 60}

// Exporter is the telemetry.Exporter this module builds. One per process.
type Exporter struct {
	cfg *telemetry.Config

	// Providers are nil for a signal that is off. The instruments below are
	// no-ops then, so a hook costs an interface call and nothing else.
	mp *sdkmetric.MeterProvider
	tp *sdktrace.TracerProvider
	lp *sdklog.LoggerProvider

	// tracer is nil when traces are off: the Begin* hooks test it and skip
	// the SDK entirely rather than start a span a sampler would drop.
	tracer trace.Tracer
	logger log.Logger
	// events is the log processor and the only place a record is dropped,
	// so it owns the drop counter. Nil when events are off.
	events *recordQueue

	statements         metric.Int64Counter
	masked             metric.Int64Counter
	auditErrors        metric.Int64Counter
	stageDuration      metric.Float64Histogram
	analyzerDuration   metric.Float64Histogram
	analyzerRetries    metric.Int64Counter
	analyzerStatements metric.Int64Counter
	connections        metric.Int64ObservableCounter
	active             metric.Int64ObservableGauge
	sessionSent        metric.Int64ObservableCounter
	sessionDropped     metric.Int64ObservableCounter
	generation         metric.Int64ObservableGauge
	meter              metric.Meter

	mu    sync.Mutex
	lanes map[string]*lane
	sent  atomic.Int64
}

// New builds the exporter from a validated telemetry block. It is a
// telemetry.Builder.
func New(cfg *telemetry.Config, res telemetry.Resource) (telemetry.Exporter, error) {
	if cfg == nil {
		return nil, errors.New("telemetry: nil config")
	}
	otel.SetErrorHandler(newRateLimitedHandler(30 * time.Second))
	otel.SetTextMapPropagator(propagation.TraceContext{})

	e := &Exporter{cfg: cfg, lanes: make(map[string]*lane)}
	r := buildResource(cfg, res)
	ctx := context.Background()

	if cfg.MetricsEnabled() {
		exp, err := newMetricExporter(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("telemetry: metrics exporter: %w", err)
		}
		e.mp = sdkmetric.NewMeterProvider(
			sdkmetric.WithResource(r),
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp,
				sdkmetric.WithInterval(time.Duration(cfg.IntervalSec())*time.Second))),
		)
		e.meter = e.mp.Meter(scopeName)
	} else {
		e.meter = noop.NewMeterProvider().Meter(scopeName)
	}
	if err := e.buildInstruments(); err != nil {
		_ = e.Shutdown(ctx)
		return nil, fmt.Errorf("telemetry: instruments: %w", err)
	}

	if cfg.TracesEnabled() {
		exp, err := newTraceExporter(ctx, cfg)
		if err != nil {
			_ = e.Shutdown(ctx)
			return nil, fmt.Errorf("telemetry: traces exporter: %w", err)
		}
		e.tp = sdktrace.NewTracerProvider(
			sdktrace.WithResource(r),
			sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.Traces.SampleRatio))),
			sdktrace.WithSpanProcessor(sdktrace.NewBatchSpanProcessor(exp)),
		)
		e.tracer = e.tp.Tracer(scopeName)
	}

	if cfg.EventsEnabled() {
		exp, err := newLogExporter(ctx, cfg)
		if err != nil {
			_ = e.Shutdown(ctx)
			return nil, fmt.Errorf("telemetry: events exporter: %w", err)
		}
		e.events = newRecordQueue(exp, cfg.QueueSize(), time.Duration(cfg.TimeoutSec())*time.Second)
		e.lp = sdklog.NewLoggerProvider(
			sdklog.WithResource(r),
			sdklog.WithProcessor(e.events),
		)
		e.logger = e.lp.Logger(scopeName)
	}
	return e, nil
}

// buildResource is the Resource every signal carries. The SDK's default
// resource (telemetry.sdk.*, a placeholder service.name) is merged by hand
// rather than through resource.Merge, because Merge refuses two resources
// with different schema URLs and the default carries semconv's; ours must
// win, since it is what tells a backend which vocabulary the attributes
// follow. Later keys override earlier ones, so the config's service.name
// beats both the default and ServiceName.
func buildResource(cfg *telemetry.Config, res telemetry.Resource) *resource.Resource {
	attrs := resource.Default().Attributes()
	attrs = append(attrs,
		attribute.String("service.name", telemetry.ServiceName),
		attribute.String("service.version", res.Version),
		attribute.String("service.instance.id", res.InstanceID),
		attribute.String("hoop.sidecar.entrypoint", res.Entrypoint),
		attribute.String("hoop.sidecar.runtime", res.Runtime),
		attribute.String("hoop.schema.version", telemetry.SchemaVersion),
	)
	if host, err := os.Hostname(); err == nil && host != "" {
		attrs = append(attrs, attribute.String("host.name", host))
	}
	for k, v := range cfg.Resource {
		attrs = append(attrs, attribute.String(k, v))
	}
	return resource.NewWithAttributes(telemetry.SchemaURL, attrs...)
}

func (e *Exporter) buildInstruments() error {
	m := e.meter
	var errs []error
	collect := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}
	var err error
	e.statements, err = m.Int64Counter("hoop.sidecar.statements",
		metric.WithDescription("Statements judged, by verdict."), metric.WithUnit("{statement}"))
	collect(err)
	e.masked, err = m.Int64Counter("hoop.sidecar.masked_values",
		metric.WithDescription("Values rewritten out of responses."), metric.WithUnit("{value}"))
	collect(err)
	e.auditErrors, err = m.Int64Counter("hoop.sidecar.audit.errors",
		metric.WithDescription("Audit events a sink could not write."), metric.WithUnit("{event}"))
	collect(err)
	e.stageDuration, err = m.Float64Histogram("hoop.sidecar.statement.duration",
		metric.WithDescription("Time a statement spent in one stage."), metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(stageBuckets...))
	collect(err)
	e.analyzerDuration, err = m.Float64Histogram("hoop.sidecar.analyzer.request.duration",
		metric.WithDescription("One model provider call."), metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(analyzerBuckets...))
	collect(err)
	e.analyzerRetries, err = m.Int64Counter("hoop.sidecar.analyzer.retries",
		metric.WithDescription("Provider calls retried after a failed attempt."), metric.WithUnit("{attempt}"))
	collect(err)
	e.analyzerStatements, err = m.Int64Counter("hoop.sidecar.analyzer.statements",
		metric.WithDescription("Statements the analyzer classified, by status and action."), metric.WithUnit("{statement}"))
	collect(err)
	e.connections, err = m.Int64ObservableCounter("hoop.sidecar.connections",
		metric.WithDescription("Connections accepted since start."), metric.WithUnit("{connection}"))
	collect(err)
	e.active, err = m.Int64ObservableGauge("hoop.sidecar.connections.active",
		metric.WithDescription("Connections open now."), metric.WithUnit("{connection}"))
	collect(err)
	e.sessionSent, err = m.Int64ObservableCounter("hoop.sidecar.session_events.sent",
		metric.WithDescription("Session events the control plane copy sent."), metric.WithUnit("{event}"))
	collect(err)
	e.sessionDropped, err = m.Int64ObservableCounter("hoop.sidecar.session_events.dropped",
		metric.WithDescription("Session events the control plane copy dropped."), metric.WithUnit("{event}"))
	collect(err)
	e.generation, err = m.Int64ObservableGauge("hoop.sidecar.config.generation",
		metric.WithDescription("Config reloads applied since start."))
	collect(err)
	return errors.Join(errs...)
}

// Lane returns the lane's recorder, building it on first call. A reload
// asks again by the same name and gets the same recorder, so its counters
// continue.
func (e *Exporter) Lane(name, protocol string) telemetry.LaneRecorder {
	e.mu.Lock()
	defer e.mu.Unlock()
	if l, ok := e.lanes[name]; ok {
		return l
	}
	l := newLane(e, name, protocol)
	e.lanes[name] = l
	return l
}

// AuditSink returns the sink that copies audit events to the collector as
// log records, nil when events are off.
func (e *Exporter) AuditSink(opts audit.SinkOptions) audit.Sink {
	if e.logger == nil {
		return nil
	}
	// statement_text off is the audit redaction applied to this copy, so a
	// collector never holds more than a file sink that redacts would.
	if !e.cfg.Events.StatementText {
		opts.RedactStatements = true
	}
	return &eventSink{e: e, opts: opts}
}

// AnalyzerCall records one provider round trip. Rare (a model call is
// seconds) so the attribute set is built per call.
func (e *Exporter) AnalyzerCall(c telemetry.AnalyzerCall) {
	ident := []attribute.KeyValue{
		attribute.String(attrAnalyzer, c.Analyzer),
		attribute.String(attrProvider, c.Provider),
		attribute.String(attrModel, c.Model),
		attribute.String(attrCallKind, c.Kind),
	}
	e.analyzerDuration.Record(context.Background(), c.Duration.Seconds(),
		metric.WithAttributeSet(attribute.NewSet(append(ident, attribute.String(attrOutcome, c.Outcome))...)))
	if c.Attempts > 1 {
		e.analyzerRetries.Add(context.Background(), int64(c.Attempts-1),
			metric.WithAttributeSet(attribute.NewSet(ident...)))
	}
}

// AnalyzerOutcome counts one classified statement.
func (e *Exporter) AnalyzerOutcome(o telemetry.AnalyzerOutcome) {
	e.analyzerStatements.Add(context.Background(), 1, metric.WithAttributeSet(attribute.NewSet(
		attribute.String(attrAnalyzer, o.Analyzer),
		attribute.String(attrStatus, o.Status),
		attribute.String(attrRiskLevel, o.RiskLevel),
		attribute.String(attrAction, o.Action),
	)))
}

// ObserveProcess registers the callback the observable instruments read on
// every collection. The lane's prebuilt set is reused when the lane has a
// recorder; a lane with no recorder (a listener that never asked for one)
// gets a set built on the spot, once a minute.
func (e *Exporter) ObserveProcess(snapshot func() telemetry.ProcessSnapshot) {
	_, err := e.meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		s := snapshot()
		for _, ls := range s.Lanes {
			set := e.laneSet(ls.Lane, ls.Protocol)
			o.ObserveInt64(e.connections, ls.Total, metric.WithAttributeSet(set))
			o.ObserveInt64(e.active, ls.Active, metric.WithAttributeSet(set))
		}
		o.ObserveInt64(e.sessionSent, s.SessionEventsSent)
		o.ObserveInt64(e.sessionDropped, s.SessionEventsDropped)
		o.ObserveInt64(e.generation, s.ConfigGeneration)
		return nil
	}, e.connections, e.active, e.sessionSent, e.sessionDropped, e.generation)
	if err != nil {
		otel.Handle(err)
	}
}

func (e *Exporter) laneSet(name, protocol string) attribute.Set {
	e.mu.Lock()
	l, ok := e.lanes[name]
	e.mu.Unlock()
	if ok && l.protocol == protocol {
		return l.set
	}
	return attribute.NewSet(attribute.String(attrLane, name), attribute.String(attrProtocol, protocol))
}

// Stats reports the audit copy's counters.
func (e *Exporter) Stats() telemetry.Stats {
	s := telemetry.Stats{EventsSent: e.sent.Load()}
	if e.events != nil {
		s.EventsDropped = e.events.dropped.Load()
	}
	return s
}

// Shutdown flushes and stops every signal, events first: they are the one
// signal whose loss an operator would notice as a missing audit row. Each
// provider's Shutdown is bounded by ctx and idempotent, so a second call is
// harmless and a dead collector costs ctx's deadline, not a hang.
func (e *Exporter) Shutdown(ctx context.Context) error {
	var errs []error
	if e.lp != nil {
		if err := e.lp.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("events: %w", err))
		}
	}
	if e.tp != nil {
		if err := e.tp.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("traces: %w", err))
		}
	}
	if e.mp != nil {
		if err := e.mp.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("metrics: %w", err))
		}
	}
	return errors.Join(errs...)
}

// Transport options.

func timeout(cfg *telemetry.Config) time.Duration {
	return time.Duration(cfg.TimeoutSec()) * time.Second
}

// rootCAs loads the pinned CA, nil for the host trust store.
func rootCAs(cfg *telemetry.Config) (*tls.Config, error) {
	if cfg.OTLP.CAFile == "" {
		return nil, nil
	}
	pem, err := os.ReadFile(cfg.OTLP.CAFile)
	if err != nil {
		return nil, fmt.Errorf("otlp.ca_file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("otlp.ca_file %q: no certificate found", cfg.OTLP.CAFile)
	}
	return &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, nil
}

// httpTarget splits the http endpoint URL into what the exporters take
// apart: host, the signal's path under the URL's path, and whether TLS is
// off (an http:// scheme, or insecure set). Each exporter is handed these
// explicitly rather than through WithEndpointURL, because the three
// exporters at the pinned versions disagree on what an empty URL path
// means: metrics and traces fall back to /v1/<signal>, otlploghttp v0.20
// posts to "/" and the collector answers 404. Validate has already checked
// the URL parses. The exporter refuses a TLS config on an insecure
// endpoint, so the CA is only applied when insecure is false.
type httpTarget struct {
	host, path string
	insecure   bool
}

func newHTTPTarget(cfg *telemetry.Config, signalPath string) httpTarget {
	u, _ := url.Parse(cfg.OTLP.Endpoint)
	return httpTarget{
		host:     u.Host,
		path:     strings.TrimSuffix(u.Path, "/") + signalPath,
		insecure: cfg.OTLP.Insecure || u.Scheme == "http",
	}
}

func newMetricExporter(ctx context.Context, cfg *telemetry.Config) (sdkmetric.Exporter, error) {
	tlsCfg, err := rootCAs(cfg)
	if err != nil {
		return nil, err
	}
	if cfg.Protocol() == telemetry.ProtocolHTTP {
		t := newHTTPTarget(cfg, "/v1/metrics")
		opts := []otlpmetrichttp.Option{
			otlpmetrichttp.WithEndpoint(t.host),
			otlpmetrichttp.WithURLPath(t.path),
			otlpmetrichttp.WithHeaders(cfg.OTLP.Headers),
			otlpmetrichttp.WithTimeout(timeout(cfg)),
		}
		if t.insecure {
			opts = append(opts, otlpmetrichttp.WithInsecure())
		} else if tlsCfg != nil {
			opts = append(opts, otlpmetrichttp.WithTLSClientConfig(tlsCfg))
		}
		return otlpmetrichttp.New(ctx, opts...)
	}
	opts := []otlpmetricgrpc.Option{
		otlpmetricgrpc.WithEndpoint(cfg.OTLP.Endpoint),
		otlpmetricgrpc.WithHeaders(cfg.OTLP.Headers),
		otlpmetricgrpc.WithTimeout(timeout(cfg)),
	}
	if cfg.OTLP.Insecure {
		opts = append(opts, otlpmetricgrpc.WithInsecure())
	} else if tlsCfg != nil {
		opts = append(opts, otlpmetricgrpc.WithTLSCredentials(credentials.NewTLS(tlsCfg)))
	}
	return otlpmetricgrpc.New(ctx, opts...)
}

func newTraceExporter(ctx context.Context, cfg *telemetry.Config) (*otlptrace.Exporter, error) {
	tlsCfg, err := rootCAs(cfg)
	if err != nil {
		return nil, err
	}
	if cfg.Protocol() == telemetry.ProtocolHTTP {
		t := newHTTPTarget(cfg, "/v1/traces")
		opts := []otlptracehttp.Option{
			otlptracehttp.WithEndpoint(t.host),
			otlptracehttp.WithURLPath(t.path),
			otlptracehttp.WithHeaders(cfg.OTLP.Headers),
			otlptracehttp.WithTimeout(timeout(cfg)),
		}
		if t.insecure {
			opts = append(opts, otlptracehttp.WithInsecure())
		} else if tlsCfg != nil {
			opts = append(opts, otlptracehttp.WithTLSClientConfig(tlsCfg))
		}
		return otlptracehttp.New(ctx, opts...)
	}
	opts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(cfg.OTLP.Endpoint),
		otlptracegrpc.WithHeaders(cfg.OTLP.Headers),
		otlptracegrpc.WithTimeout(timeout(cfg)),
	}
	if cfg.OTLP.Insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	} else if tlsCfg != nil {
		opts = append(opts, otlptracegrpc.WithTLSCredentials(credentials.NewTLS(tlsCfg)))
	}
	return otlptracegrpc.New(ctx, opts...)
}

func newLogExporter(ctx context.Context, cfg *telemetry.Config) (sdklog.Exporter, error) {
	tlsCfg, err := rootCAs(cfg)
	if err != nil {
		return nil, err
	}
	if cfg.Protocol() == telemetry.ProtocolHTTP {
		t := newHTTPTarget(cfg, "/v1/logs")
		opts := []otlploghttp.Option{
			otlploghttp.WithEndpoint(t.host),
			otlploghttp.WithURLPath(t.path),
			otlploghttp.WithHeaders(cfg.OTLP.Headers),
			otlploghttp.WithTimeout(timeout(cfg)),
		}
		if t.insecure {
			opts = append(opts, otlploghttp.WithInsecure())
		} else if tlsCfg != nil {
			opts = append(opts, otlploghttp.WithTLSClientConfig(tlsCfg))
		}
		return otlploghttp.New(ctx, opts...)
	}
	opts := []otlploggrpc.Option{
		otlploggrpc.WithEndpoint(cfg.OTLP.Endpoint),
		otlploggrpc.WithHeaders(cfg.OTLP.Headers),
		otlploggrpc.WithTimeout(timeout(cfg)),
	}
	if cfg.OTLP.Insecure {
		opts = append(opts, otlploggrpc.WithInsecure())
	} else if tlsCfg != nil {
		opts = append(opts, otlploggrpc.WithTLSCredentials(credentials.NewTLS(tlsCfg)))
	}
	return otlploggrpc.New(ctx, opts...)
}

// rateLimitedHandler is the SDK's error handler. Export failures arrive
// once per interval per signal, forever, while a collector is down; one
// slog line per distinct message per window keeps the sidecar's own log
// readable through an outage. Written at Warn: the data path is unaffected.
type rateLimitedHandler struct {
	window time.Duration
	mu     sync.Mutex
	last   map[string]time.Time
}

func newRateLimitedHandler(window time.Duration) *rateLimitedHandler {
	return &rateLimitedHandler{window: window, last: make(map[string]time.Time)}
}

func (h *rateLimitedHandler) Handle(err error) {
	if err == nil {
		return
	}
	msg := err.Error()
	now := time.Now()
	h.mu.Lock()
	if t, ok := h.last[msg]; ok && now.Sub(t) < h.window {
		h.mu.Unlock()
		return
	}
	h.last[msg] = now
	// A message that embeds a changing detail (a port, a request id) would
	// grow the map one entry per failure; expired entries go when it gets
	// big.
	if len(h.last) > 64 {
		for k, t := range h.last {
			if now.Sub(t) >= h.window {
				delete(h.last, k)
			}
		}
	}
	h.mu.Unlock()
	slog.Warn("telemetry: export failed", "err", msg)
}
