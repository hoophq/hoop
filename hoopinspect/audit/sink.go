package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
	"unicode/utf8"
)

// DefaultMaxStatementBytes caps a persisted statement when SinkOptions leaves
// MaxStatementBytes unset. A generated ORM query or a bulk INSERT can run to
// megabytes, and an audit line that large costs more to store and index than
// the answer it holds is worth.
const DefaultMaxStatementBytes = 8192

// DefaultMemoryCapacity is the ring size a MemorySink uses when the caller
// asks for a non-positive capacity.
const DefaultMemoryCapacity = 1024

// truncationMarker is appended to a statement the sink cut short, so a reader
// can tell "this query ended here" from "this query was clipped".
const truncationMarker = "...[truncated]"

var (
	// ErrSinkClosed is returned by Write after Close. Returning an error
	// rather than panicking matters because Close races with in-flight
	// connections during shutdown, and a panic on the data path would take
	// down sessions that were about to finish cleanly.
	ErrSinkClosed = errors.New("audit: sink is closed")

	// ErrQueueFull is returned by AsyncSink.Write when the queue has no room.
	//
	// The alternative designs are both worse. Blocking would make a slow
	// audit store stall the user's query, which is the problem AsyncSink
	// exists to solve. Dropping silently would take the decision away from
	// the caller — and the caller is the gate, which treats a failed audit
	// write as a policy failure, because an unrecorded statement is exactly
	// the one an attacker wants.
	ErrQueueFull = errors.New("audit: sink queue is full")
)

// SinkOptions controls what a sink persists.
//
// The zero value is usable: full statement text, capped at
// DefaultMaxStatementBytes, stamped with time.Now.
type SinkOptions struct {
	// RedactStatements replaces Event.Statement (and Event.HTTP.Body) with a
	// non-reversible fingerprint instead of the text.
	//
	// Some shops cannot store query text at all, because literals embed the
	// very PII the database is regulated for. A fingerprint still answers
	// the questions an audit trail is for: which statement repeated, how
	// often, and whether the one that ran at 03:00 is the one that ran at
	// 09:00.
	RedactStatements bool

	// MaxStatementBytes truncates Event.Statement beyond this many bytes.
	// Defaults to DefaultMaxStatementBytes when <= 0.
	MaxStatementBytes int

	// Now supplies the timestamp for events that arrive without one.
	// Defaults to time.Now. Injectable so a test can assert on an exact
	// line rather than on a regex.
	Now func() time.Time
}

func (o SinkOptions) maxStatementBytes() int {
	if o.MaxStatementBytes <= 0 {
		return DefaultMaxStatementBytes
	}
	return o.MaxStatementBytes
}

func (o SinkOptions) now() time.Time {
	if o.Now == nil {
		return time.Now()
	}
	return o.Now()
}

// apply returns ev with the statement policy and timestamp normalization
// applied. It takes ev by value and never mutates anything the caller still
// owns.
func (o SinkOptions) apply(ev Event) Event {
	if ev.Timestamp.IsZero() {
		ev.Timestamp = o.now()
	}
	// Normalize to UTC so JSONL from several hosts sorts lexicographically.
	// A line carrying a local offset silently interleaves wrong.
	ev.Timestamp = ev.Timestamp.UTC()

	if o.RedactStatements {
		// Fingerprint the ORIGINAL text, before any truncation. Hashing the
		// truncated form would make the fingerprint depend on
		// MaxStatementBytes, so raising the limit would break correlation
		// against every record written under the old limit.
		if ev.Statement != "" {
			ev.Statement = fingerprint(ev.Statement)
		}
		if ev.HTTP != nil && ev.HTTP.Body != "" {
			// Copy: the HTTPDetail is shared with the caller's Statement and
			// possibly with a sibling sink in a MultiSink. Redacting in
			// place would rewrite someone else's data.
			detail := *ev.HTTP
			detail.Body = fingerprint(detail.Body)
			ev.HTTP = &detail
		}
		return ev
	}

	ev.Statement = truncate(ev.Statement, o.maxStatementBytes())
	return ev
}

// fingerprint returns a stable, non-reversible tag for s.
//
// 64 bits of hex is short enough to eyeball in a log line and wide enough
// that a collision between two statements in one deployment is not a
// practical concern; this is a correlation key, not a security boundary.
func fingerprint(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:8])
}

// truncate cuts s to at most max bytes and marks it.
//
// The cut backs off to a rune boundary: slicing mid-sequence would make the
// JSON encoder emit U+FFFD, which corrupts a statement that was merely long.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return s[:max] + truncationMarker
}

// flusher is satisfied by *bufio.Writer.
type flusher interface{ Flush() error }

// syncer is satisfied by *os.File.
type syncer interface{ Sync() error }

// JSONLSink writes one JSON object per line.
//
// JSONL rather than a JSON array because an audit file is appended to for the
// life of a process and read by `grep`, `jq -c` and `tail -f`. An array needs
// a closing bracket the writer may never get to write.
type JSONLSink struct {
	opts SinkOptions

	mu     sync.Mutex
	w      io.Writer
	enc    *json.Encoder
	closed bool
}

// NewJSONLSink writes events to w.
//
// The sink owns serialization but not the writer's lifetime: Close flushes
// what it can reach but never closes w, because the common w is os.Stdout.
func NewJSONLSink(w io.Writer, opts SinkOptions) *JSONLSink {
	enc := json.NewEncoder(w)
	// Audit records hold SQL and URLs. Escaping <, > and & to \u003c turns a
	// `WHERE age > 30` into something nobody can grep for.
	enc.SetEscapeHTML(false)
	return &JSONLSink{opts: opts, w: w, enc: enc}
}

// Write encodes ev as one line.
//
// ctx is accepted for the Sink interface and deliberately ignored: an audit
// record must not be skipped because the user's request context was
// cancelled, since a cancelled request is a plausible thing to want the
// record of.
func (s *JSONLSink) Write(_ context.Context, ev Event) error {
	ev = s.opts.apply(ev)

	// The whole encode is under the lock. json.Encoder is not safe for
	// concurrent use, and even if it were, two goroutines interleaving
	// writes produce a torn line — a corrupt audit record that a parser
	// either rejects or, worse, reads as a different event.
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrSinkClosed
	}
	if err := s.enc.Encode(ev); err != nil {
		return fmt.Errorf("audit: encoding event: %w", err)
	}
	return nil
}

// Close flushes the writer when it buffers, and is safe to call twice.
func (s *JSONLSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true

	var errs []error
	// Flush before Sync: bytes still in a *bufio.Writer are not yet in the
	// file, so syncing first would durably persist nothing.
	if f, ok := s.w.(flusher); ok {
		if err := f.Flush(); err != nil {
			errs = append(errs, fmt.Errorf("audit: flushing sink: %w", err))
		}
	}
	if sy, ok := s.w.(syncer); ok {
		if err := sy.Sync(); err != nil {
			errs = append(errs, fmt.Errorf("audit: syncing sink: %w", err))
		}
	}
	return errors.Join(errs...)
}

// MultiSink fans one event out to several sinks — typically a local JSONL
// file plus a remote collector.
type MultiSink struct {
	sinks []Sink

	mu     sync.Mutex
	closed bool
}

// NewMultiSink fans out to sinks in order.
func NewMultiSink(sinks ...Sink) *MultiSink {
	return &MultiSink{sinks: sinks}
}

// Write attempts every sink even after one fails, then returns the joined
// error.
//
// Stopping at the first failure would let a broken local sink silently
// disable a remote one: the file fills its disk, the write errors, and the
// SIEM that InfoSec actually watches stops receiving events without anyone
// changing a config.
func (m *MultiSink) Write(ctx context.Context, ev Event) error {
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return ErrSinkClosed
	}

	var errs []error
	for _, s := range m.sinks {
		if err := s.Write(ctx, ev); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Close closes every sink, joining errors. Safe to call twice.
func (m *MultiSink) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	m.mu.Unlock()

	var errs []error
	for _, s := range m.sinks {
		if err := s.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// MemorySink keeps the most recent events in a bounded ring, for tests and
// for a debug endpoint that answers "what just happened on this process".
//
// It is explicitly NOT durable and must never be the only sink in a
// deployment that needs an audit trail. When the ring is full the OLDEST
// event is evicted and Write still succeeds: this sink exists to show recent
// activity, and failing a user's query because a debug buffer wrapped would
// be absurd. Everything that must survive goes to a JSONLSink beside it.
type MemorySink struct {
	mu      sync.Mutex
	buf     []Event
	start   int // index of the oldest retained event
	n       int // number retained
	dropped int
	closed  bool
}

// NewMemorySink returns a ring holding capacity events, or
// DefaultMemoryCapacity when capacity <= 0.
func NewMemorySink(capacity int) *MemorySink {
	if capacity <= 0 {
		capacity = DefaultMemoryCapacity
	}
	return &MemorySink{buf: make([]Event, capacity)}
}

// Write appends ev, evicting the oldest event when the ring is full.
func (m *MemorySink) Write(_ context.Context, ev Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrSinkClosed
	}

	if m.n == len(m.buf) {
		m.buf[m.start] = ev
		m.start = (m.start + 1) % len(m.buf)
		m.dropped++
		return nil
	}
	m.buf[(m.start+m.n)%len(m.buf)] = ev
	m.n++
	return nil
}

// Events returns the retained events, oldest first, as a fresh slice so a
// caller iterating them cannot be raced by a concurrent Write.
func (m *MemorySink) Events() []Event {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]Event, m.n)
	for i := range m.n {
		out[i] = m.buf[(m.start+i)%len(m.buf)]
	}
	return out
}

// Len reports how many events are retained.
func (m *MemorySink) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.n
}

// Dropped reports how many events the ring evicted. A debug endpoint that
// shows the buffer without showing this number is lying about coverage.
func (m *MemorySink) Dropped() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dropped
}

// Close marks the sink closed. Retained events stay readable, because the
// reason to close a debug buffer is shutdown and that is exactly when
// somebody wants to look at it. Safe to call twice.
func (m *MemorySink) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

// AsyncSink hands events to a drain goroutine so a slow backing store — an
// HTTP collector, a remote file system — does not sit on the connection's
// data path while a user waits for their query.
type AsyncSink struct {
	inner Sink
	queue chan Event
	done  chan struct{}

	mu     sync.Mutex
	closed bool

	// errMu guards the drain goroutine's failure record. Only the first
	// error is kept: a backing store that is down fails for every event, and
	// joining ten thousand identical errors helps nobody.
	errMu    sync.Mutex
	firstErr error
	failures int

	closeOnce sync.Once
	closeErr  error
}

// NewAsyncSink wraps inner with a bounded queue.
//
// queueSize is the number of events that may be in flight; <= 0 means a
// synchronous handoff, where Write succeeds only if the drain goroutine is
// idle and ready.
func NewAsyncSink(inner Sink, queueSize int) *AsyncSink {
	if queueSize < 0 {
		queueSize = 0
	}
	a := &AsyncSink{
		inner: inner,
		queue: make(chan Event, queueSize),
		done:  make(chan struct{}),
	}
	go a.drain()
	return a
}

func (a *AsyncSink) drain() {
	defer close(a.done)
	for ev := range a.queue {
		// Background, not the originating request's context: by the time an
		// event drains, the request that produced it has usually finished
		// and cancelled its context. Honouring that would discard the record
		// of every query the user walked away from.
		if err := a.inner.Write(context.Background(), ev); err != nil {
			a.errMu.Lock()
			if a.firstErr == nil {
				a.firstErr = err
			}
			a.failures++
			a.errMu.Unlock()
		}
	}
}

// Write enqueues ev without blocking, returning ErrQueueFull when the queue
// has no room.
func (a *AsyncSink) Write(_ context.Context, ev Event) error {
	// The lock is held across the send so Close cannot close the queue
	// underneath an in-flight Write, which would panic. It is never held for
	// long: the send is non-blocking.
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return ErrSinkClosed
	}
	select {
	case a.queue <- ev:
		return nil
	default:
		return ErrQueueFull
	}
}

// Close stops accepting events, drains what is queued, then closes inner.
// Safe to call twice; the second call returns the first call's result.
//
// It also reports the drain goroutine's write failures, which have no other
// route back to a caller: by the time an async write fails, the statement it
// described has long since run.
func (a *AsyncSink) Close() error {
	a.closeOnce.Do(func() {
		a.mu.Lock()
		a.closed = true
		close(a.queue)
		a.mu.Unlock()

		// Waiting happens outside the lock, so a Write racing with Close
		// returns ErrSinkClosed instead of blocking until the drain is done.
		<-a.done

		var errs []error
		a.errMu.Lock()
		if a.firstErr != nil {
			errs = append(errs, fmt.Errorf("audit: %d async write(s) failed: %w", a.failures, a.firstErr))
		}
		a.errMu.Unlock()
		if err := a.inner.Close(); err != nil {
			errs = append(errs, err)
		}
		a.closeErr = errors.Join(errs...)
	})
	return a.closeErr
}

var (
	_ Sink = (*JSONLSink)(nil)
	_ Sink = (*MultiSink)(nil)
	_ Sink = (*MemorySink)(nil)
	_ Sink = (*AsyncSink)(nil)
)
