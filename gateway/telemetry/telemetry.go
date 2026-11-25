// Package telemetry pushes the gateway's own OpenTelemetry signals to an
// OTLP collector: sidecar handshakes, the session event ingest, the
// database pool and API latency per route, plus spans on the handshake and
// the ingest. It is push only: nothing here serves a scrape endpoint.
//
// It is off unless OTEL_EXPORTER_OTLP_ENDPOINT is set (appconfig). Off, every
// hook is one atomic load and a nil check.
//
// No global provider is set. The gateway links client libraries that read
// the global tracer and meter providers (gRPC, cloud SDKs), and their spans
// would then ride this exporter with this resource. The providers stay
// here. The SDK error handler is global by nature; it is rate-limited so a
// dead collector cannot flood the log.
package telemetry

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/appconfig"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/credentials"
)

// SchemaVersion is the version of the vocabulary this package exports,
// carried as hoop.schema.version and in SchemaURL. An attribute or
// instrument added bumps the minor; one renamed or removed bumps the major,
// because a dashboard written against the old name goes dark.
const SchemaVersion = "1.0"

// SchemaURL names the vocabulary on the resource.
const SchemaURL = "https://hoop.dev/schemas/gateway-telemetry/" + SchemaVersion

// ServiceName is the service.name the gateway reports unless
// OTEL_SERVICE_NAME overrides it.
const ServiceName = "hoop-gateway"

// scopeName names the meter and the tracer.
const scopeName = "github.com/hoophq/hoop/gateway"

// ShutdownTimeout bounds the final flush: a collector that is down must not
// hold the process past its termination grace period.
const ShutdownTimeout = 3 * time.Second

// durationBuckets are the bounds of every duration histogram here, in
// seconds: the OpenTelemetry HTTP server advice, plus 15 and 30 s, the
// sidecar's request timeout, so a handshake that waited on the database
// pool does not land in +Inf.
var durationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10, 15, 30}

// exporter is one started export: its providers and the instruments the
// hooks record into.
type exporter struct {
	mp *sdkmetric.MeterProvider
	// tp and tracer are nil when OTEL_TRACES_SAMPLER is unset: the hooks
	// then start no span.
	tp     *sdktrace.TracerProvider
	tracer trace.Tracer

	handshakes        metric.Int64Counter
	handshakeDuration metric.Float64Histogram
	batches           metric.Int64Counter
	events            metric.Int64Counter
	batchDuration     metric.Float64Histogram
	httpDuration      metric.Float64Histogram
}

var current atomic.Pointer[exporter]

// Start builds the exporters cfg names and starts pushing. A nil cfg is
// export off, and Start does nothing. db is the pool the gauges read; nil
// registers none.
func Start(cfg *appconfig.OTLPExport, version string, db *sql.DB) error {
	if cfg == nil {
		return nil
	}
	otel.SetErrorHandler(newRateLimitedHandler(30 * time.Second))
	// The SDK's internal logger prints whole URLs (a URL it cannot parse,
	// for one) to stderr. Nothing of it reaches the gateway log.
	otel.SetLogger(sdkLogger)
	ctx := context.Background()
	mexp, err := newMetricExporter(ctx, cfg)
	if err != nil {
		return fmt.Errorf("telemetry: metrics exporter: %w", err)
	}
	reader := sdkmetric.NewPeriodicReader(mexp, sdkmetric.WithInterval(cfg.Interval))
	var spans sdktrace.SpanProcessor
	if cfg.Sampler != "" {
		texp, err := newTraceExporter(ctx, cfg)
		if err != nil {
			_ = reader.Shutdown(ctx)
			return fmt.Errorf("telemetry: traces exporter: %w", err)
		}
		spans = sdktrace.NewBatchSpanProcessor(texp)
	}
	e, err := newExporter(buildResource(cfg, version), reader, spans, sampler(cfg), db)
	if err != nil {
		return err
	}
	current.Store(e)
	// The host only: an endpoint path can carry a vendor key, the reason
	// appconfig keeps the endpoint out of its errors.
	log.Infof("telemetry enabled, host=%s, protocol=%s, interval=%s, traces=%v",
		cfg.Host, cfg.Protocol, cfg.Interval, cfg.Sampler != "")
	return nil
}

// newExporter builds the providers over reader and spans, the seam a test
// fills with an in-memory reader and span recorder. A nil spans starts no
// span.
func newExporter(res *resource.Resource, reader sdkmetric.Reader, spans sdktrace.SpanProcessor,
	sampler sdktrace.Sampler, db *sql.DB) (*exporter, error) {
	e := &exporter{mp: sdkmetric.NewMeterProvider(sdkmetric.WithResource(res), sdkmetric.WithReader(reader))}
	if spans != nil {
		e.tp = sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
			sdktrace.WithSampler(sampler),
			sdktrace.WithSpanProcessor(spans),
		)
		e.tracer = e.tp.Tracer(scopeName)
	}
	err := e.buildInstruments(e.mp.Meter(scopeName), db)
	if err != nil {
		_ = e.shutdown(context.Background())
		return nil, fmt.Errorf("telemetry: instruments: %w", err)
	}
	return e, nil
}

func (e *exporter) buildInstruments(m metric.Meter, db *sql.DB) error {
	var errs []error
	collect := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}
	buckets := metric.WithExplicitBucketBoundaries(durationBuckets...)
	var err error
	e.handshakes, err = m.Int64Counter("hoop.gateway.sidecar.handshakes",
		metric.WithDescription("Sidecar handshakes answered, by outcome."), metric.WithUnit("{handshake}"))
	collect(err)
	e.handshakeDuration, err = m.Float64Histogram("hoop.gateway.sidecar.handshake.duration",
		metric.WithDescription("Time to answer one sidecar handshake."), metric.WithUnit("s"), buckets)
	collect(err)
	e.batches, err = m.Int64Counter("hoop.gateway.session_events.batches",
		metric.WithDescription("Session event batches sidecars sent, by outcome."), metric.WithUnit("{batch}"))
	collect(err)
	e.events, err = m.Int64Counter("hoop.gateway.session_events.events",
		metric.WithDescription("Session events in the batches that were read, by outcome."), metric.WithUnit("{event}"))
	collect(err)
	e.batchDuration, err = m.Float64Histogram("hoop.gateway.session_events.batch.duration",
		metric.WithDescription("Time to answer one session event batch."), metric.WithUnit("s"), buckets)
	collect(err)
	e.httpDuration, err = m.Float64Histogram("http.server.request.duration",
		metric.WithDescription("Duration of HTTP server requests."), metric.WithUnit("s"), buckets)
	collect(err)
	if db != nil {
		collect(observePool(m, db))
	}
	return errors.Join(errs...)
}

// observePool registers the gauges sql.DBStats feeds, read on every
// collection. Waits are cumulative, so they are counters: a rate of
// hoop.gateway.db.connections.waits above zero is a pool too small for the
// load.
func observePool(m metric.Meter, db *sql.DB) error {
	conn := metric.WithUnit("{connection}")
	open, err1 := m.Int64ObservableGauge("hoop.gateway.db.connections.open",
		metric.WithDescription("Connections open, in use and idle."), conn)
	inUse, err2 := m.Int64ObservableGauge("hoop.gateway.db.connections.in_use",
		metric.WithDescription("Connections running a query."), conn)
	idle, err3 := m.Int64ObservableGauge("hoop.gateway.db.connections.idle",
		metric.WithDescription("Connections open and unused."), conn)
	maxOpen, err4 := m.Int64ObservableGauge("hoop.gateway.db.connections.max",
		metric.WithDescription("The pool cap, POSTGRES_MAX_OPEN_CONNS."), conn)
	waits, err5 := m.Int64ObservableCounter("hoop.gateway.db.connections.waits",
		metric.WithDescription("Queries that waited for a free connection."), metric.WithUnit("{wait}"))
	waited, err6 := m.Float64ObservableCounter("hoop.gateway.db.connections.wait_duration",
		metric.WithDescription("Total time queries waited for a free connection."), metric.WithUnit("s"))
	if err := errors.Join(err1, err2, err3, err4, err5, err6); err != nil {
		return err
	}
	_, err := m.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		s := db.Stats()
		o.ObserveInt64(open, int64(s.OpenConnections))
		o.ObserveInt64(inUse, int64(s.InUse))
		o.ObserveInt64(idle, int64(s.Idle))
		o.ObserveInt64(maxOpen, int64(s.MaxOpenConnections))
		o.ObserveInt64(waits, s.WaitCount)
		o.ObserveFloat64(waited, s.WaitDuration.Seconds())
		return nil
	}, open, inUse, idle, maxOpen, waits, waited)
	return err
}

// Shutdown flushes and stops the export, bounded by ctx. The hooks go back
// to doing nothing first, so a request in flight does not record into a
// provider that is shutting down. Safe to call when export never started.
// Its error is the bounded description of describe, never an exporter's
// text, so a caller may log it.
func Shutdown(ctx context.Context) error {
	e := current.Swap(nil)
	if e == nil {
		return nil
	}
	if err := e.shutdown(ctx); err != nil {
		return errors.New(describe(err))
	}
	return nil
}

func (e *exporter) shutdown(ctx context.Context) error {
	var errs []error
	if e.tp != nil {
		if err := e.tp.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("traces: %w", err))
		}
	}
	if err := e.mp.Shutdown(ctx); err != nil {
		errs = append(errs, fmt.Errorf("metrics: %w", err))
	}
	return errors.Join(errs...)
}

// buildResource is the resource every signal carries. The SDK's default
// resource is merged by hand rather than through resource.Merge, which
// refuses two schema URLs; ours must win. Later keys override earlier
// ones, and OTEL_SERVICE_NAME beats service.name in OTEL_RESOURCE_ATTRIBUTES,
// as the OpenTelemetry spec says.
func buildResource(cfg *appconfig.OTLPExport, version string) *resource.Resource {
	attrs := resource.Default().Attributes()
	attrs = append(attrs,
		attribute.String("service.name", ServiceName),
		attribute.String("service.version", version),
		attribute.String("service.instance.id", uuid.NewString()),
		attribute.String("hoop.schema.version", SchemaVersion),
	)
	if host, err := os.Hostname(); err == nil && host != "" {
		attrs = append(attrs, attribute.String("host.name", host))
	}
	for k, v := range cfg.Resource {
		attrs = append(attrs, attribute.String(k, v))
	}
	if cfg.ServiceName != "" {
		attrs = append(attrs, attribute.String("service.name", cfg.ServiceName))
	}
	return resource.NewWithAttributes(SchemaURL, attrs...)
}

// sampler maps OTEL_TRACES_SAMPLER, which appconfig validated.
func sampler(cfg *appconfig.OTLPExport) sdktrace.Sampler {
	switch cfg.Sampler {
	case "always_on":
		return sdktrace.AlwaysSample()
	case "always_off":
		return sdktrace.NeverSample()
	case "traceidratio":
		return sdktrace.TraceIDRatioBased(cfg.SamplerArg)
	case "parentbased_always_off":
		return sdktrace.ParentBased(sdktrace.NeverSample())
	case "parentbased_traceidratio":
		return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SamplerArg))
	default:
		return sdktrace.ParentBased(sdktrace.AlwaysSample())
	}
}

// clientTLS is the TLS config of a secure export: the pinned CA, else the
// host trust store. Nil for an insecure one.
func clientTLS(cfg *appconfig.OTLPExport) (*tls.Config, error) {
	if cfg.Insecure {
		return nil, nil
	}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.CAFile == "" {
		return tlsCfg, nil
	}
	pem, err := os.ReadFile(cfg.CAFile)
	if err != nil {
		return nil, fmt.Errorf("OTEL_EXPORTER_OTLP_CERTIFICATE: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("OTEL_EXPORTER_OTLP_CERTIFICATE %q: no certificate found", cfg.CAFile)
	}
	tlsCfg.RootCAs = pool
	return tlsCfg, nil
}

// endpointURL is the export target with its scheme. The SDK exporters read
// the OTEL_* variables again on their own, and apply
// OTEL_EXPORTER_OTLP_INSECURE after the endpoint's scheme. Every exporter
// is therefore handed the full URL and an explicit secure or insecure
// option, so appconfig's decision is the one that holds. net/url builds it,
// so a path that holds a % stays escaped and the SDK can parse it.
func endpointURL(cfg *appconfig.OTLPExport, signalPath string) string {
	u := url.URL{Scheme: "https", Host: cfg.Host, Path: cfg.Path + signalPath}
	if cfg.Insecure {
		u.Scheme = "http"
	}
	if signalPath == "" {
		u.Path = ""
	}
	return u.String()
}

// httpClient is the OTLP/HTTP exporters' client: the TLS choice made here,
// and a recorder that keeps the status of the last answer, so a failed
// export can name it without its text. The SDK ignores WithTimeout once it
// is handed a client, so the client carries OTEL_EXPORTER_OTLP_TIMEOUT.
func httpClient(tlsCfg *tls.Config, timeout time.Duration) (*http.Client, *statusRecorder) {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.TLSClientConfig = tlsCfg
	rec := &statusRecorder{next: t}
	return &http.Client{Transport: rec, Timeout: timeout}, rec
}

func newMetricExporter(ctx context.Context, cfg *appconfig.OTLPExport) (sdkmetric.Exporter, error) {
	tlsCfg, err := clientTLS(cfg)
	if err != nil {
		return nil, err
	}
	if cfg.Protocol == appconfig.OTLPProtocolHTTP {
		client, rec := httpClient(tlsCfg, cfg.Timeout)
		opts := []otlpmetrichttp.Option{
			otlpmetrichttp.WithEndpointURL(endpointURL(cfg, "/v1/metrics")),
			otlpmetrichttp.WithHeaders(cfg.Headers),
			otlpmetrichttp.WithTimeout(cfg.Timeout),
			otlpmetrichttp.WithHTTPClient(client),
		}
		if cfg.Insecure {
			opts = append(opts, otlpmetrichttp.WithInsecure())
		} else {
			opts = append(opts, otlpmetrichttp.WithTLSClientConfig(tlsCfg))
		}
		exp, err := otlpmetrichttp.New(ctx, opts...)
		if err != nil {
			return nil, err
		}
		return metricExporter{Exporter: exp, rec: rec}, nil
	}
	opts := []otlpmetricgrpc.Option{
		otlpmetricgrpc.WithEndpointURL(endpointURL(cfg, "")),
		otlpmetricgrpc.WithHeaders(cfg.Headers),
		otlpmetricgrpc.WithTimeout(cfg.Timeout),
	}
	if cfg.Insecure {
		opts = append(opts, otlpmetricgrpc.WithInsecure())
	} else {
		opts = append(opts, otlpmetricgrpc.WithTLSCredentials(credentials.NewTLS(tlsCfg)))
	}
	exp, err := otlpmetricgrpc.New(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return metricExporter{Exporter: exp}, nil
}

func newTraceExporter(ctx context.Context, cfg *appconfig.OTLPExport) (sdktrace.SpanExporter, error) {
	tlsCfg, err := clientTLS(cfg)
	if err != nil {
		return nil, err
	}
	if cfg.Protocol == appconfig.OTLPProtocolHTTP {
		client, rec := httpClient(tlsCfg, cfg.Timeout)
		opts := []otlptracehttp.Option{
			otlptracehttp.WithEndpointURL(endpointURL(cfg, "/v1/traces")),
			otlptracehttp.WithHeaders(cfg.Headers),
			otlptracehttp.WithTimeout(cfg.Timeout),
			otlptracehttp.WithHTTPClient(client),
		}
		if cfg.Insecure {
			opts = append(opts, otlptracehttp.WithInsecure())
		} else {
			opts = append(opts, otlptracehttp.WithTLSClientConfig(tlsCfg))
		}
		exp, err := otlptracehttp.New(ctx, opts...)
		if err != nil {
			return nil, err
		}
		return spanExporter{SpanExporter: exp, rec: rec}, nil
	}
	opts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpointURL(endpointURL(cfg, "")),
		otlptracegrpc.WithHeaders(cfg.Headers),
		otlptracegrpc.WithTimeout(cfg.Timeout),
	}
	if cfg.Insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	} else {
		opts = append(opts, otlptracegrpc.WithTLSCredentials(credentials.NewTLS(tlsCfg)))
	}
	exp, err := otlptracegrpc.New(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return spanExporter{SpanExporter: exp}, nil
}

// warnf is where the error handler writes; a test captures it.
var warnf = log.Warnf

// rateLimitedHandler is the SDK's error handler. It never prints an error's
// text, which can hold the endpoint URL or a collector's answer: only the
// bounded description of describe. Export failures arrive once per
// interval per signal while a collector is down; one log line per distinct
// description per window keeps the gateway's log readable.
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
	msg := describe(err)
	now := time.Now()
	h.mu.Lock()
	if t, ok := h.last[msg]; ok && now.Sub(t) < h.window {
		h.mu.Unlock()
		return
	}
	h.last[msg] = now
	h.mu.Unlock()
	warnf("telemetry: %s; the collector has the details", msg)
}
