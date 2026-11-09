package daemon

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/hoophq/hoop/sidecar/analytics"
	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/audit"
	telem "github.com/hoophq/hoop/sidecar/telemetry"
)

// NewTelemetryExporter builds the OpenTelemetry exporter a "telemetry" block
// asks for. The daemon cannot import the SDK (its module carries a
// dependency tree the root does not), so the binary that links
// sidecar/telemetry/otlp injects the constructor, the same way it injects
// LoadCodecPlugin and YAMLFromJSON. Nil in a build that linked none, which
// checkTelemetry turns into a refusal of the block.
var NewTelemetryExporter telem.Builder

// checkTelemetry refuses a "telemetry" block this process cannot serve. It
// runs in Run and Validate, not in Config.Validate: the gateway validates
// sidecar documents with the same decoder and links no exporter.
func checkTelemetry(cfg *Config) error {
	if cfg.Telemetry == nil {
		return nil
	}
	if NewTelemetryExporter == nil {
		return errors.New(`config has a "telemetry" block but this build has no OpenTelemetry exporter; ` +
			"build github.com/hoophq/hoop/sidecar/cmd, or remove the block")
	}
	return nil
}

// otelExporter is the process's exporter with the daemon-side glue around
// it: the lane recorders handed to servers, the observers the analyzer calls
// and the hook the outbound transports read. Every method is nil-receiver
// safe, so a process with no telemetry block calls through a nil pointer and
// does nothing, the same contract the usage analytics holder keeps.
type otelExporter struct {
	exp telem.Exporter
	log *slog.Logger
}

// telemetryShutdownTimeout bounds the final flush. Short on purpose: a
// collector that is down must not hold the pod past its termination grace
// period, and the metrics it would have received describe a process that is
// gone.
const telemetryShutdownTimeout = 3 * time.Second

// newOTelExporter builds the exporter for cfg, or nil when the block is
// absent. The resource is what usage analytics already derives for the
// process, so the two identities agree.
func newOTelExporter(cfg *Config, log *slog.Logger) (*otelExporter, error) {
	if cfg.Telemetry == nil {
		return nil, nil
	}
	if NewTelemetryExporter == nil {
		// checkTelemetry ran first; this is the belt for a caller that
		// skipped it.
		return nil, checkTelemetry(cfg)
	}
	res := telem.Resource{
		Version:    Version,
		InstanceID: sidecarID(cfg),
		Entrypoint: cfg.entrypoint,
		Runtime:    analytics.Runtime(),
	}
	if res.Entrypoint == "" {
		res.Entrypoint = analytics.EntrypointEmbedded
	}
	exp, err := NewTelemetryExporter(cfg.Telemetry, res)
	if err != nil {
		return nil, err
	}
	o := &otelExporter{exp: exp, log: log}
	outboundInject.Store(&injectFunc{exp.Inject})
	log.Info("telemetry enabled",
		"endpoint", cfg.Telemetry.OTLP.Endpoint,
		"protocol", cfg.Telemetry.Protocol(),
		"metrics", cfg.Telemetry.MetricsEnabled(),
		"traces", cfg.Telemetry.TracesEnabled(),
		"events", cfg.Telemetry.EventsEnabled(),
		"statement_text", cfg.Telemetry.Events != nil && cfg.Telemetry.Events.StatementText)
	return o, nil
}

// sidecarID is the install identity usage analytics reports, see
// newTelemetry for the chain. Shared so the OTel service.instance.id and
// the analytics sidecar-id name one install.
func sidecarID(cfg *Config) string {
	id := analytics.IDFromEnv()
	if id == "" && cfg.cp != nil {
		id = cfg.cp.analyticsID()
	}
	if id == "" && cfg.configPath != "" {
		id = analytics.IDFromHost(absPath(cfg.configPath))
	}
	if id == "" {
		id = analytics.IDFromHost()
	}
	return id
}

// lane returns a lane's recorder, or a nil interface: a typed nil pointer
// would reach the gate as non-nil and be called through.
func (o *otelExporter) lane(name, protocol string) telem.LaneRecorder {
	if o == nil {
		return nil
	}
	return o.exp.Lane(name, protocol)
}

// auditSink is the sink that turns audit events into log records, or a nil
// interface when events are off or there is no exporter.
func (o *otelExporter) auditSink(opts audit.SinkOptions) audit.Sink {
	if o == nil {
		return nil
	}
	return o.exp.AuditSink(opts)
}

// analyzerCall and analyzerOutcome mirror the analyzer's observers onto the
// exporter. Both are nil-receiver safe.
func (o *otelExporter) analyzerCall(provider, model string) func(analyzer.Call) {
	if o == nil {
		return nil
	}
	return func(c analyzer.Call) {
		o.exp.AnalyzerCall(telem.AnalyzerCall{
			Analyzer: c.Rule,
			Provider: provider,
			Model:    model,
			Kind:     string(callKind(c)),
			Outcome:  string(c.Outcome),
			Duration: c.Duration,
			Timeout:  c.Timeout,
			Attempts: c.Attempts,
		})
	}
}

func (o *otelExporter) analyzerOutcome() func(analyzer.Outcome) {
	if o == nil {
		return nil
	}
	return func(out analyzer.Outcome) {
		o.exp.AnalyzerOutcome(telem.AnalyzerOutcome{
			Analyzer:  out.Rule,
			Status:    out.Status,
			RiskLevel: string(out.RiskLevel),
			Action:    string(out.Action),
		})
	}
}

// observe registers the callback the process gauges read on each export.
func (o *otelExporter) observe(snapshot func() telem.ProcessSnapshot) {
	if o != nil {
		o.exp.ObserveProcess(snapshot)
	}
}

// stats is what GET /stats reports under "telemetry", or nil.
func (o *otelExporter) stats() *telem.Stats {
	if o == nil {
		return nil
	}
	s := o.exp.Stats()
	return &s
}

// close flushes and stops the exporter, bounded, and unhooks the outbound
// transports. It runs LAST in Run: after every server closed and after the
// audit sink closed, because the exporter's log sink is a member of that
// chain and an async audit sink drains into it on its own Close. Shutting
// the SDK down first would lose the tail of the trail the chain was still
// handing over. The defer order in Run encodes this; keep them paired.
func (o *otelExporter) close() {
	if o == nil {
		return
	}
	outboundInject.Store(nil)
	ctx, cancel := context.WithTimeout(context.Background(), telemetryShutdownTimeout)
	defer cancel()
	if err := o.exp.Shutdown(ctx); err != nil {
		o.log.Warn("telemetry shutdown reported an error", "error", err)
	}
}

// injectFunc is the exporter's Inject, held behind a pointer so the atomic
// can hold nil.
type injectFunc struct {
	inject func(context.Context, http.Header)
}

// outboundInject is read by every outbound HTTP transport this process
// builds (withUserAgent), so an OPA decision or a model call made under a
// stage span carries the trace to the service it calls. Process-wide for
// the same reason the W3C propagator is a global in OpenTelemetry: the
// transports are built before and independently of the exporter, by code
// paths (buildPolicy, buildAnalyzer, the descriptor fetchers) that have no
// business knowing it exists. Nil until an exporter is built, and after it
// closes; a request under no span writes nothing either way.
var outboundInject atomic.Pointer[injectFunc]

// tracePropagationTransport injects the trace context, when an exporter is
// up, then hands the request on. Reading the hook per request rather than
// at construction is what lets a client built before Run's exporter still
// propagate, and a test's client never need one.
type tracePropagationTransport struct {
	next http.RoundTripper
}

func (t tracePropagationTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	hook := outboundInject.Load()
	if hook == nil {
		return t.next.RoundTrip(req)
	}
	// RoundTrip must not modify the request it was given.
	r := req.Clone(req.Context())
	hook.inject(r.Context(), r.Header)
	return t.next.RoundTrip(r)
}
