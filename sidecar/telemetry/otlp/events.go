package otlp

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/hoophq/hoop/sidecar/audit"
)

// exportMaxBatchSize is how many records one export request carries.
const exportMaxBatchSize = 512

// exportInterval is how long a partial batch waits for company before it
// goes alone.
const exportInterval = time.Second

// eventSink is the audit.Sink that copies events to the collector as log
// records. It is the best-effort copy the audit package doc describes:
// Write applies the sink options, builds the record, hands it to the SDK
// and returns nil. The SDK logger is non-blocking (its processor is the
// recordQueue below, which drops rather than waits), so a dead collector
// never reaches the statement that produced the event.
type eventSink struct {
	e    *Exporter
	opts audit.SinkOptions
}

func (s *eventSink) Write(ctx context.Context, ev audit.Event) error {
	ev = s.opts.Apply(ev)
	// ctx is the caller's: the SDK reads the span in it, which is what
	// ties the record to the statement span in a trace backend.
	s.e.logger.Emit(ctx, record(ev))
	s.e.sent.Add(1)
	return nil
}

// Close is a no-op. The records already sit in the exporter's queue, which
// the daemon closes through Exporter.Shutdown after the whole audit chain
// has closed; a Close here that waited on the collector would hold the
// chain's shutdown on a network timeout, and a MultiSink closes its
// members in order. Exporter.Shutdown is the flush.
func (*eventSink) Close() error { return nil }

func severity(kind audit.Kind) (log.Severity, string) {
	switch kind {
	case audit.KindViolation, audit.KindMasked:
		return log.SeverityWarn, "WARN"
	case audit.KindError:
		return log.SeverityError, "ERROR"
	default:
		return log.SeverityInfo, "INFO"
	}
}

// record maps one event to a log record. The body is the kind, so a
// backend's "message" column reads statement / violation / masked, and
// everything else is an attribute a query can filter on. ev has been
// through SinkOptions.Apply, so Statement is already the fingerprint when
// the sink redacts.
func record(ev audit.Event) log.Record {
	var r log.Record
	r.SetTimestamp(ev.Timestamp)
	r.SetBody(log.StringValue(string(ev.Kind)))
	sev, text := severity(ev.Kind)
	r.SetSeverity(sev)
	r.SetSeverityText(text)

	attrs := make([]log.KeyValue, 0, 16+len(ev.Metadata))
	str := func(k, v string) {
		if v != "" {
			attrs = append(attrs, log.String(k, v))
		}
	}
	str(attrLane, ev.Connection)
	str(attrProtocol, string(ev.Protocol))
	str(attrSessionID, string(ev.SessionID))
	str(attrPrincipal, ev.Principal)
	str(attrOperation, string(ev.Operation))
	str(attrDirection, string(ev.Direction))
	switch ev.Kind {
	case audit.KindStatement, audit.KindViolation:
		verdict := "allow"
		if !ev.Allowed {
			verdict = "deny"
		}
		attrs = append(attrs, log.String(attrVerdict, verdict))
	}
	str(attrRule, ev.Rule)
	str(attrMessage, ev.Message)
	str(attrStatement, ev.Statement)
	if len(ev.Tables) > 0 {
		attrs = append(attrs, log.Slice(attrTables, stringValues(ev.Tables)...))
	}
	str(attrError, ev.Error)
	if len(ev.MaskedEntities) > 0 {
		attrs = append(attrs, log.Slice(attrMaskedEntities, stringValues(ev.MaskedEntities)...))
	}
	if ev.MaskedCount > 0 {
		attrs = append(attrs, log.Int(attrMaskedCount, ev.MaskedCount))
	}
	if ev.Kind == audit.KindSessionEnd {
		attrs = append(attrs,
			log.Int64(attrDurationMS, ev.Duration.Milliseconds()),
			log.Int(attrStatementCount, ev.StatementCount),
			log.Int(attrDeniedCount, ev.DeniedCount),
		)
	}
	if h := ev.HTTP; h != nil {
		str(attrHTTPMethod, h.Method)
		str(attrHTTPPath, h.Path)
		if h.StatusCode != 0 {
			attrs = append(attrs, log.Int(attrHTTPStatusCode, h.StatusCode))
		}
		str(attrHTTPHost, h.Host)
		str(attrHTTPResource, h.Resource)
	}
	for k, v := range ev.Metadata {
		attrs = append(attrs, log.String(attrMetaPrefix+k, v))
	}
	if id := ev.Metadata["review_id"]; id != "" {
		attrs = append(attrs, log.String(attrReviewID, id))
	}
	r.AddAttributes(attrs...)
	return r
}

func stringValues(ss []string) []log.Value {
	vs := make([]log.Value, len(ss))
	for i, s := range ss {
		vs[i] = log.StringValue(s)
	}
	return vs
}

// recordQueue is the log processor between the SDK logger and the OTLP
// exporter: a bounded queue that drops the oldest record when full and
// counts every drop, drained by one goroutine that exports in batches.
//
// It stands in for the SDK's BatchProcessor, which does the same thing but
// keeps its drop count to itself (it logs a warning and resets it). The
// contract puts that number in Stats, because an operator looking at a gap
// in the audit copy needs to know whether the sidecar dropped records or
// the collector lost them, and a count only this queue can see is the only
// way to answer that exactly.
type recordQueue struct {
	exp     sdklog.Exporter
	ch      chan sdklog.Record
	timeout time.Duration
	dropped atomic.Int64

	flush chan chan struct{}
	stop  chan struct{}
	done  chan struct{}
	once  sync.Once
}

func newRecordQueue(exp sdklog.Exporter, size int, timeout time.Duration) *recordQueue {
	q := &recordQueue{
		exp:     exp,
		ch:      make(chan sdklog.Record, size),
		timeout: timeout,
		flush:   make(chan chan struct{}),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	go q.run()
	return q
}

func (*recordQueue) Enabled(context.Context, sdklog.EnabledParameters) bool { return true }

// OnEmit enqueues a copy of r without waiting. When the queue is full the
// oldest record goes, not the newest: the newest is the one that says what
// is happening now, and a collector that recovers wants the recent end.
func (q *recordQueue) OnEmit(_ context.Context, r *sdklog.Record) error {
	rec := r.Clone()
	for {
		select {
		case q.ch <- rec:
			return nil
		default:
		}
		select {
		case <-q.ch:
			q.dropped.Add(1)
		default:
		}
	}
}

func (q *recordQueue) run() {
	defer close(q.done)
	batch := make([]sdklog.Record, 0, exportMaxBatchSize)
	for {
		batch = batch[:0]
		select {
		case r := <-q.ch:
			batch = append(batch, r)
		case reply := <-q.flush:
			q.export(q.drain(batch))
			close(reply)
			continue
		case <-q.stop:
			q.drainAll(batch)
			return
		}
		// Collect company for the first record: up to a full batch, or
		// until the interval passes.
		timer := time.NewTimer(exportInterval)
	fill:
		for len(batch) < exportMaxBatchSize {
			select {
			case r := <-q.ch:
				batch = append(batch, r)
			case <-timer.C:
				break fill
			case reply := <-q.flush:
				q.export(q.drain(batch))
				close(reply)
				batch = batch[:0]
				break fill
			case <-q.stop:
				timer.Stop()
				q.drainAll(batch)
				return
			}
		}
		timer.Stop()
		q.export(batch)
	}
}

// drain appends what is queued right now to batch, up to a full batch.
func (q *recordQueue) drain(batch []sdklog.Record) []sdklog.Record {
	for len(batch) < exportMaxBatchSize {
		select {
		case r := <-q.ch:
			batch = append(batch, r)
		default:
			return batch
		}
	}
	return batch
}

// drainAll exports everything queued, batch by batch. Each export is
// bounded by the exporter timeout, and once Shutdown has stopped the
// exporter every call returns at once, so the loop ends.
func (q *recordQueue) drainAll(batch []sdklog.Record) {
	for {
		batch = q.drain(batch)
		if len(batch) == 0 {
			return
		}
		q.export(batch)
		batch = batch[:0]
	}
}

func (q *recordQueue) export(batch []sdklog.Record) {
	if len(batch) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), q.timeout)
	defer cancel()
	if err := q.exp.Export(ctx, batch); err != nil {
		otel.Handle(err)
	}
}

// ForceFlush exports what is queued and waits for it, bounded by ctx.
func (q *recordQueue) ForceFlush(ctx context.Context) error {
	reply := make(chan struct{})
	select {
	case q.flush <- reply:
	case <-q.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-reply:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Shutdown stops intake, waits for the goroutine to export what it holds
// (bounded by ctx; the goroutine finishes on its own afterwards against a
// stopped exporter) and shuts the exporter down.
func (q *recordQueue) Shutdown(ctx context.Context) error {
	q.once.Do(func() { close(q.stop) })
	var err error
	select {
	case <-q.done:
	case <-ctx.Done():
		err = ctx.Err()
	}
	if serr := q.exp.Shutdown(ctx); serr != nil && err == nil {
		err = serr
	}
	return err
}
