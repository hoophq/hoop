// Package telemetry is the contract between the data path and an
// OpenTelemetry exporter.
//
// The exporter itself lives in the nested sidecar/telemetry/otlp module,
// because the OpenTelemetry SDK and its OTLP exporters are a dependency tree
// the root module does not carry (sidecar/CLAUDE.md: one dependency). This
// package declares what the data path can report and nothing about how it
// leaves the process, so gate, proxy, policy and daemon stay free of the
// SDK and a build that links no exporter pays nothing: every hook is an
// interface the daemon leaves nil.
//
// Two rules hold on every hook. It is called on the data path, so an
// implementation costs one instrument record with a prebuilt attribute set
// and never blocks. And it carries no statement text: the audit trail is
// the record of what ran; telemetry says how much, how fast and with what
// verdict. Principal and session id ride on spans and log records, never
// on a metric, where they would be a cardinality bomb.
package telemetry

import (
	"context"
	"net/http"
	"time"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/inspect"
)

// SchemaVersion is the version of the attribute and instrument vocabulary
// this package defines, carried on every signal as the hoop.schema.version
// resource attribute and in SchemaURL. An attribute or instrument added
// bumps the minor; one renamed or removed bumps the major, because a
// dashboard written against the old name goes dark.
const SchemaVersion = "1.0"

// SchemaURL identifies the schema on the Resource, the OpenTelemetry way
// of saying which vocabulary a producer speaks.
const SchemaURL = "https://hoop.dev/schemas/sidecar-telemetry/" + SchemaVersion

// ServiceName is the service.name every sidecar reports unless the
// resource block overrides it.
const ServiceName = "hoop-sidecar"

// Stage names one slice of the time a statement spends in the sidecar.
// Each is recorded as hoop.sidecar.statement.duration with hoop.stage set.
type Stage string

const (
	// StagePolicy is a local rule set or an OPA round trip: the cost the
	// sidecar adds to decide. Engine says which.
	StagePolicy Stage = "policy"
	// StageAnalyzer is the model-backed evaluator, cache hits included;
	// the provider call alone is hoop.sidecar.analyzer.request.duration.
	StageAnalyzer Stage = "analyzer"
	// StageReview is the hold while a human decides. Its own stage so a
	// ten-minute approval does not read as a slow policy.
	StageReview Stage = "review"
	// StageUpstream is from the request leaving the sidecar to its answer
	// arriving. Pairing says how exact the measurement is.
	StageUpstream Stage = "upstream"
)

// Engines a StagePolicy record names in hoop.engine.
const (
	EngineRules = "rules"
	EngineOPA   = "opa"
)

// Pairing says how a request was matched to its response for an upstream
// measurement, carried as hoop.pairing.
type Pairing string

const (
	// PairingExact is a protocol that pairs them itself: one gRPC call,
	// or HTTP/1 where responses answer requests in order.
	PairingExact Pairing = "exact"
	// PairingUnpipelined is a byte-path protocol whose codec does not
	// pair responses to requests. The sample is taken only when exactly
	// one request is outstanding and dropped when the client pipelines,
	// so the series describes unpipelined exchanges alone.
	PairingUnpipelined Pairing = "unpipelined"
)

// Review outcomes a StageReview record names, in place of allow and deny.
// A fixed set: a status the plane may add later lands in ReviewDenied
// rather than minting a series a dashboard was not written for.
const (
	ReviewApproved     = "approved"
	ReviewRejected     = "rejected"
	ReviewExpired      = "expired"
	ReviewRevoked      = "revoked"
	ReviewDisconnected = "disconnected"
	ReviewDenied       = "denied"
)

// Stage outcomes for every stage but review.
const (
	OutcomeAllow = "allow"
	OutcomeDeny  = "deny"
	OutcomeError = "error"
)

// ExchangeKind names the span that encloses a client's exchange.
type ExchangeKind string

const (
	// ExchangeSession is one byte-path connection; its statements are
	// child spans.
	ExchangeSession ExchangeKind = "hoop.session"
	// ExchangeRPC is one gRPC call on a lane that terminates HTTP/2.
	ExchangeRPC ExchangeKind = "hoop.rpc"
)

// Attr is one string attribute. Strings only: that is all the data path
// has to say, and a struct of two strings allocates nothing when passed
// in a variadic the implementation reads once.
type Attr struct {
	Key, Value string
}

// Verdict is what a statement's evaluation came to, for the statement
// counter and the statement span.
type Verdict struct {
	// Denied is what the client saw, audit refusal included.
	Denied bool
	// Source is the kind of evaluator that denied: a policy.Verdict.Source
	// or one of the gate's own. Empty when allowed.
	Source string
	// Rule names the rule that denied, when one did.
	Rule string
	// RiskLevel is the analyzer's classification when one ran.
	RiskLevel string
	// Err is an infrastructure failure the decision continued past.
	Err error
}

// EndExchange closes an exchange span. err is the transport's failure,
// nil for a normal end.
type EndExchange func(err error)

// EndStatement closes a statement span and counts the statement.
type EndStatement func(Verdict)

// EndStage records a stage's duration and closes its span. outcome is one
// of the Outcome* or Review* values.
type EndStage func(outcome string, err error)

// StageRecorder is the part of a LaneRecorder the policy chain reads: it
// times each evaluator and the review hold. Declared apart so policy
// depends on nothing it does not call.
type StageRecorder interface {
	// BeginStage opens a stage under ctx. The returned context carries the
	// stage's span, so an evaluator that makes an HTTP call (OPA, a model
	// provider) propagates it. Call the returned function exactly once.
	BeginStage(ctx context.Context, stage Stage, engine string) (context.Context, EndStage)
}

// LaneRecorder is what one lane's data path reports into. Every connection
// of the lane shares one, so an implementation is safe for concurrent use
// and holds the lane's attributes prebuilt.
type LaneRecorder interface {
	StageRecorder

	// BeginExchange opens the span for one exchange. The returned context
	// is the one the exchange's statements are judged under.
	BeginExchange(ctx context.Context, kind ExchangeKind, attrs ...Attr) (context.Context, EndExchange)

	// BeginStatement opens the span for one judged statement and counts
	// it when ended. stmt is read for its operation, direction and HTTP
	// detail; its text is never recorded.
	//
	// By value, not by pointer: a pointer handed to an interface method
	// escapes, and the gate would then copy every statement to the heap
	// on the off path too. The copy costs a stack write; the allocation
	// would cost the data path.
	BeginStatement(ctx context.Context, stmt inspect.Statement) (context.Context, EndStatement)

	// Upstream records one request-to-response measurement.
	Upstream(ctx context.Context, d time.Duration, pairing Pairing)

	// Masked records values rewritten out of one response.
	Masked(values int)

	// AuditError records one audit event a sink could not write.
	AuditError()
}

// AnalyzerCall is one provider call, as analyzer.Call reports it. Declared
// here rather than imported because analyzer imports policy, which imports
// this package.
type AnalyzerCall struct {
	// Analyzer is the lane name for an analyzer block, the rule name for
	// the deprecated rule form.
	Analyzer        string
	Provider, Model string
	// Outcome is ok, error or timeout.
	Outcome  string
	Duration time.Duration
	Timeout  time.Duration
	Attempts int
}

// AnalyzerOutcome is what happened to one eligible statement.
type AnalyzerOutcome struct {
	Analyzer  string
	Status    string
	RiskLevel string
	Action    string
}

// LaneStats is one listener's connection counters, as its server reports
// them.
type LaneStats struct {
	Lane, Protocol string
	Active, Total  int64
}

// ProcessSnapshot is what the exporter reads on every metrics collection
// for the gauges no data-path hook produces.
type ProcessSnapshot struct {
	Lanes []LaneStats
	// ConfigGeneration counts applied reloads, zero at startup.
	ConfigGeneration int64
	// SessionEventsSent and SessionEventsDropped are the plane copy's
	// counters; zero without a control plane.
	SessionEventsSent, SessionEventsDropped int64
}

// Resource is what the exporter says about this process on every signal.
type Resource struct {
	// Version is the release this process reports.
	Version string
	// InstanceID is the sidecar id usage analytics derives: stable across
	// restarts, hashed, never a token.
	InstanceID string
	// Entrypoint is hoop, hoop-inspect or embedded.
	Entrypoint string
	// Runtime is linux, docker, kubernetes, macos or windows.
	Runtime string
}

// Stats is what the exporter reports about itself for GET /stats.
type Stats struct {
	// EventsSent and EventsDropped count audit events the log exporter
	// accepted and dropped. Zero when events are off.
	EventsSent, EventsDropped int64
}

// Exporter is one process's exporter, built once by the daemon from the
// telemetry block and shut down with the process.
type Exporter interface {
	// Lane returns the recorder for a listener. Called once per lane at
	// startup; a reload keeps the recorder, so counters survive it.
	Lane(name, protocol string) LaneRecorder

	// AuditSink returns the sink that turns audit events into log
	// records, or nil when events are off. It applies opts like every
	// other sink and never fails a write (audit package doc).
	AuditSink(opts audit.SinkOptions) audit.Sink

	// AnalyzerCall and AnalyzerOutcome mirror the analyzer's observers.
	AnalyzerCall(AnalyzerCall)
	AnalyzerOutcome(AnalyzerOutcome)

	// ObserveProcess registers the callback the gauges read. Called once.
	ObserveProcess(func() ProcessSnapshot)

	// Inject writes the trace context in ctx into h as W3C traceparent,
	// so an outbound call made under a stage span (OPA, a model provider)
	// joins the trace. Nothing is written when ctx carries no span.
	Inject(ctx context.Context, h http.Header)

	// Stats reports the exporter's own counters.
	Stats() Stats

	// Shutdown flushes and stops. Bounded by ctx.
	Shutdown(ctx context.Context) error
}

// Builder constructs an Exporter. The daemon holds one, injected by the
// binary that links the nested module.
type Builder func(cfg *Config, res Resource) (Exporter, error)
