package otlp

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/telemetry"
)

// lane is one listener's telemetry.LaneRecorder. Every connection of the
// lane shares it, so everything here is immutable after newLane or behind
// a sync.Map.
//
// The attribute sets a hook records with are cached by their enum values
// (direction, operation, verdict, stage, outcome: a few dozen combinations
// per lane at most) as ready-made option slices, so after the first
// statement of a kind the hot path passes a prebuilt slice and the SDK
// finds its aggregation by set hash. No map, no slice and no sort per
// statement.
type lane struct {
	e              *Exporter
	name, protocol string

	// set is {lane, protocol}, the base of every metric and the
	// observable callback's key.
	set       attribute.Set
	setOpt    []metric.AddOption
	laneOpt   []metric.AddOption // {lane}: audit errors are per lane only
	spanAttrs []attribute.KeyValue

	statementOpts sync.Map // statementKey -> []metric.AddOption
	stageOpts     sync.Map // stageKey -> []metric.RecordOption
	upstreamOpts  sync.Map // telemetry.Pairing -> []metric.RecordOption
}

type statementKey struct {
	direction inspect.Direction
	operation inspect.Operation
	denied    bool
	source    string
}

type stageKey struct {
	stage   telemetry.Stage
	engine  string
	outcome string
}

func newLane(e *Exporter, name, protocol string) *lane {
	l := &lane{e: e, name: name, protocol: protocol}
	l.spanAttrs = []attribute.KeyValue{
		attribute.String(attrLane, name),
		attribute.String(attrProtocol, protocol),
	}
	l.set = attribute.NewSet(l.spanAttrs...)
	l.setOpt = []metric.AddOption{metric.WithAttributeSet(l.set)}
	l.laneOpt = []metric.AddOption{metric.WithAttributeSet(attribute.NewSet(attribute.String(attrLane, name)))}
	return l
}

// cached returns the value under key, building it once. sync.Map's
// LoadOrStore makes a duplicate build harmless: both racers store an
// equal set and one wins.
func cached[K comparable, V any](m *sync.Map, key K, build func(K) V) V {
	if v, ok := m.Load(key); ok {
		return v.(V)
	}
	v, _ := m.LoadOrStore(key, build(key))
	return v.(V)
}

func (l *lane) statementOpt(k statementKey) []metric.AddOption {
	return cached(&l.statementOpts, k, func(k statementKey) []metric.AddOption {
		attrs := make([]attribute.KeyValue, 0, 6)
		attrs = append(attrs, l.spanAttrs...)
		attrs = append(attrs,
			attribute.String(attrDirection, string(k.direction)),
			attribute.String(attrOperation, string(k.operation)),
		)
		if k.denied {
			attrs = append(attrs,
				attribute.String(attrVerdict, telemetry.OutcomeDeny),
				attribute.String(attrDenySource, k.source),
			)
		} else {
			attrs = append(attrs, attribute.String(attrVerdict, telemetry.OutcomeAllow))
		}
		return []metric.AddOption{metric.WithAttributeSet(attribute.NewSet(attrs...))}
	})
}

func (l *lane) stageOpt(k stageKey) []metric.RecordOption {
	return cached(&l.stageOpts, k, func(k stageKey) []metric.RecordOption {
		attrs := make([]attribute.KeyValue, 0, 5)
		attrs = append(attrs, l.spanAttrs...)
		attrs = append(attrs, attribute.String(attrStage, string(k.stage)))
		if k.stage == telemetry.StagePolicy && k.engine != "" {
			attrs = append(attrs, attribute.String(attrEngine, k.engine))
		}
		attrs = append(attrs, attribute.String(attrOutcome, k.outcome))
		return []metric.RecordOption{metric.WithAttributeSet(attribute.NewSet(attrs...))}
	})
}

func (l *lane) upstreamOpt(p telemetry.Pairing) []metric.RecordOption {
	return cached(&l.upstreamOpts, p, func(p telemetry.Pairing) []metric.RecordOption {
		attrs := make([]attribute.KeyValue, 0, 4)
		attrs = append(attrs, l.spanAttrs...)
		attrs = append(attrs,
			attribute.String(attrStage, string(telemetry.StageUpstream)),
			attribute.String(attrPairing, string(p)),
		)
		return []metric.RecordOption{metric.WithAttributeSet(attribute.NewSet(attrs...))}
	})
}

// spanWanted is true when a span started under ctx could be recorded. With
// traces off there is no tracer. Under an unsampled parent the ParentBased
// sampler would drop the child anyway, so the hook skips the SDK, which
// is what makes a 1% sample ratio cost 1% and not 100% of the span setup.
func (l *lane) spanWanted(ctx context.Context) bool {
	if l.e.tracer == nil {
		return false
	}
	parent := trace.SpanContextFromContext(ctx)
	return !parent.IsValid() || parent.IsSampled()
}

// endNothing is the closure returned when no span was opened; one shared
// value so the hook allocates nothing for it.
func endNothing(error) {}

func (l *lane) BeginExchange(ctx context.Context, kind telemetry.ExchangeKind, attrs ...telemetry.Attr) (context.Context, telemetry.EndExchange) {
	if !l.spanWanted(ctx) {
		return ctx, endNothing
	}
	kvs := make([]attribute.KeyValue, 0, len(l.spanAttrs)+len(attrs))
	kvs = append(kvs, l.spanAttrs...)
	for _, a := range attrs {
		kvs = append(kvs, attribute.String(a.Key, a.Value))
	}
	ctx, span := l.e.tracer.Start(ctx, string(kind),
		trace.WithSpanKind(trace.SpanKindServer), trace.WithAttributes(kvs...))
	return ctx, func(err error) {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}
}

func (l *lane) BeginStatement(ctx context.Context, stmt inspect.Statement) (context.Context, telemetry.EndStatement) {
	direction, operation := stmt.Direction, stmt.Operation
	if !l.spanWanted(ctx) {
		return ctx, func(v telemetry.Verdict) {
			l.count(direction, operation, v)
		}
	}
	kvs := make([]attribute.KeyValue, 0, 7)
	kvs = append(kvs,
		attribute.String(attrDirection, string(direction)),
		attribute.String(attrOperation, string(operation)),
	)
	if h := stmt.HTTP; h != nil {
		if h.Method != "" {
			kvs = append(kvs, attribute.String(attrHTTPMethod, h.Method))
		}
		if h.Path != "" {
			kvs = append(kvs, attribute.String(attrHTTPPath, h.Path))
		}
		if h.StatusCode != 0 {
			kvs = append(kvs, attribute.Int(attrHTTPStatusCode, h.StatusCode))
		}
	}
	if stmt.Database != "" {
		kvs = append(kvs, attribute.String(attrDatabase, stmt.Database))
	}
	if len(stmt.Tables) > 0 {
		kvs = append(kvs, attribute.StringSlice(attrTables, stmt.Tables))
	}
	ctx, span := l.e.tracer.Start(ctx, "hoop.statement",
		trace.WithSpanKind(trace.SpanKindInternal), trace.WithAttributes(kvs...))
	return ctx, func(v telemetry.Verdict) {
		verdict := telemetry.OutcomeAllow
		if v.Denied {
			verdict = telemetry.OutcomeDeny
		}
		span.SetAttributes(attribute.String(attrVerdict, verdict))
		if v.Denied {
			span.SetAttributes(attribute.String(attrDenySource, v.Source))
			span.SetStatus(codes.Error, v.Rule)
		}
		if v.Rule != "" {
			span.SetAttributes(attribute.String(attrRule, v.Rule))
		}
		if v.RiskLevel != "" {
			span.SetAttributes(attribute.String(attrRiskLevel, v.RiskLevel))
		}
		if v.Err != nil {
			span.RecordError(v.Err)
		}
		span.End()
		l.count(direction, operation, v)
	}
}

func (l *lane) count(direction inspect.Direction, operation inspect.Operation, v telemetry.Verdict) {
	k := statementKey{direction: direction, operation: operation, denied: v.Denied}
	if v.Denied {
		k.source = v.Source
	}
	l.e.statements.Add(context.Background(), 1, l.statementOpt(k)...)
}

func (l *lane) BeginStage(ctx context.Context, stage telemetry.Stage, engine string) (context.Context, telemetry.EndStage) {
	start := time.Now()
	if !l.spanWanted(ctx) {
		return ctx, func(outcome string, _ error) {
			l.e.stageDuration.Record(context.Background(), time.Since(start).Seconds(),
				l.stageOpt(stageKey{stage: stage, engine: engine, outcome: outcome})...)
		}
	}
	var kvs []attribute.KeyValue
	if engine != "" {
		kvs = append(kvs, attribute.String(attrEngine, engine))
	}
	ctx, span := l.e.tracer.Start(ctx, string(stage),
		trace.WithSpanKind(trace.SpanKindInternal), trace.WithAttributes(kvs...))
	return ctx, func(outcome string, err error) {
		span.SetAttributes(attribute.String(attrOutcome, outcome))
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
		l.e.stageDuration.Record(context.Background(), time.Since(start).Seconds(),
			l.stageOpt(stageKey{stage: stage, engine: engine, outcome: outcome})...)
	}
}

// Upstream records the measurement and, when the caller is inside a
// recorded span, marks it there too as an event: the statement span then
// shows where its time went without a child span per round trip.
func (l *lane) Upstream(ctx context.Context, d time.Duration, pairing telemetry.Pairing) {
	l.e.stageDuration.Record(context.Background(), d.Seconds(), l.upstreamOpt(pairing)...)
	if span := trace.SpanFromContext(ctx); span.IsRecording() {
		span.AddEvent("upstream", trace.WithAttributes(
			attribute.Int64(attrDurationMS, d.Milliseconds()),
			attribute.String(attrPairing, string(pairing)),
		))
	}
}

func (l *lane) Masked(values int) {
	l.e.masked.Add(context.Background(), int64(values), l.setOpt...)
}

func (l *lane) AuditError() {
	l.e.auditErrors.Add(context.Background(), 1, l.laneOpt...)
}
