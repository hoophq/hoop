package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/session"
)

// The session events contract. The sidecar POSTs its audit events to the
// plane, which records them as sessions; the local JSONL file stays the
// record of truth, and this is a copy that may lose events under pressure.
//
// The body is SessionEventsRequest. The answer:
//
//	2xx  applied, or applied before. The events leave the queue.
//	4xx  never resend. 413 is a batch over the limits below; 412 is a plane
//	     whose organization has the feature off.
//	5xx  resend later, the same events with the same seq. The plane ignores
//	     a seq it already applied, so a resend is safe.
//
// A plane says it takes events with SessionEventsHeader on the handshake,
// and this sidecar names CapabilitySessionEvents in CapabilitiesHeader.
const (
	// controlPlaneSessionEventsPath is appended to the base URL.
	controlPlaneSessionEventsPath = "/api/sidecars/events"

	// MaxSessionEventsBatch is the most events one request carries. The plane
	// answers 413 above it. Exported because the plane enforces what the
	// sidecar honours, and one constant is a contract where two literals are
	// a coincidence.
	MaxSessionEventsBatch = 500
	// MaxSessionEventsBatchBytes is the largest body one request carries.
	// The plane answers 413 above it. An event that alone does not fit is
	// dropped here and counted; with the default 8 KiB statement cap only an
	// HTTP body near this size can be one.
	MaxSessionEventsBatchBytes = 4 << 20
)

// SessionEventsRequest is the body of POST /api/sidecars/events.
type SessionEventsRequest struct {
	Events []SessionEvent `json:"events"`
}

// SessionEvent is one audit event and its place in its session.
type SessionEvent struct {
	// Seq numbers the events of one sidecar session: 1 for the first, then
	// one more for each event after it. The plane stores the highest it
	// applied and ignores any event at or below it, which is what makes a
	// resend idempotent. A gap is allowed: it is an event this sidecar
	// dropped.
	Seq int64 `json:"seq"`
	// Event is the record exactly as the JSONL sink writes it: the same
	// redaction and the same statement cap.
	Event audit.Event `json:"event"`
}

const (
	// sessionEventsQueueBytes bounds the memory the queue holds. Past it the
	// oldest events are dropped: the plane is a copy, and a sidecar that
	// grew without bound while its gateway was down would trade the data
	// path for the copy.
	sessionEventsQueueBytes = 16 << 20
	// sessionEventsQueueEvents bounds the queue's length, so a burst of tiny
	// events cannot hold millions of slices under the byte bound.
	sessionEventsQueueEvents = 20000

	// sessionEventsFlushEvery is the longest an event waits for a batch to
	// fill. A full batch goes at once.
	sessionEventsFlushEvery = time.Second
	// sessionEventsBackoffMin and sessionEventsBackoffMax bound the wait
	// before a resend after a 5xx or an unreachable plane.
	sessionEventsBackoffMin = time.Second
	sessionEventsBackoffMax = time.Minute
	// sessionEventsCloseTimeout bounds the last flush at shutdown. A plane
	// that is down at shutdown does not hold the process: the events are in
	// the JSONL file.
	sessionEventsCloseTimeout = 5 * time.Second
	// sessionEventsDropLogEvery spaces the warning about dropped events, and
	// the error about refused ones. One line per batch would flood the log at
	// the moment it matters.
	sessionEventsDropLogEvery = time.Minute
	// sessionEventsSeqIdle is how long a session's counter outlives its last
	// event, and sessionEventsSeqSweepEvery how often that is checked. See
	// sweepSeqs.
	sessionEventsSeqIdle       = 24 * time.Hour
	sessionEventsSeqSweepEvery = 10 * time.Minute
	// maxSessionEventsResponse bounds the answer read. The plane answers a
	// short JSON object.
	maxSessionEventsResponse = 64 << 10
)

// The body around the entries. Each entry is one SessionEvent, already
// encoded, so the size of a batch is the sum of what it holds.
var (
	sessionEventsBodyPrefix = []byte(`{"events":[`)
	sessionEventsBodySuffix = []byte(`]}`)
)

// maxSessionEventEntry is the largest encoded SessionEvent that fits in a
// batch on its own.
var maxSessionEventEntry = MaxSessionEventsBatchBytes - len(sessionEventsBodyPrefix) - len(sessionEventsBodySuffix)

// queuedEvent is one encoded SessionEvent. pos is its place in the stream of
// everything ever queued, so an acknowledgement still names the right events
// after a full queue dropped some of the ones in flight.
type queuedEvent struct {
	pos   uint64
	entry []byte
}

// seqCounter is the last seq a session was given, and when.
type seqCounter struct {
	seq int64
	at  time.Time
}

// inflightBatch is the batch out on the wire. An event a full queue evicts
// from it is not counted until the answer says whether the plane took it.
type inflightBatch struct {
	active      bool
	first, last uint64
	evicted     int
}

// sessionEventsStats is what /stats reports about the sink.
type sessionEventsStats struct {
	Enabled     bool `json:"enabled"`
	Queued      int  `json:"queued"`
	QueuedBytes int  `json:"queued_bytes"`
	// Sent counts events the plane answered 2xx for.
	Sent int64 `json:"sent"`
	// Dropped counts events that never reached the plane because of this
	// process: evicted from a full queue, too large for a batch, or queued
	// when the plane stopped taking events. Each event is counted once, as
	// sent, dropped or rejected.
	Dropped int64 `json:"dropped"`
	// Rejected counts events the plane answered with a 4xx.
	Rejected int64 `json:"rejected"`
	// Retries counts batches resent after a 5xx or a failed request.
	Retries int64 `json:"retries"`
}

// sessionEventSink sends audit events to the control plane.
//
// It is an audit.Sink whose Write never fails and never blocks. The gate
// refuses a statement whose audit write fails when fail_on_audit_error is
// set, so a sink that returned an error for a slow gateway would turn the
// gateway's health into the database's. Write encodes the event, numbers it
// and puts it in a bounded queue; one goroutine sends the queue in batches.
// A full queue drops its oldest event and counts it.
//
// It numbers every event of every session even while the plane takes none.
// A session that was open when the plane turned the feature on must keep
// counting from where it was: restarting at 1 would make the plane, which
// may hold a higher seq for it, ignore everything after.
type sessionEventSink struct {
	cp   *controlPlane
	opts audit.SinkOptions
	log  *slog.Logger
	http *http.Client

	enabled atomic.Bool

	mu sync.Mutex
	// seqs is the last seq given to each open session. A session leaves it
	// with its session_end event, or after sessionEventsSeqIdle without one.
	seqs      map[session.ID]seqCounter
	lastSweep time.Time
	// queue holds the events not yet acknowledged, oldest first. next is the
	// pos the next queued event gets; queue[0].pos is the oldest held.
	queue    []queuedEvent
	next     uint64
	bytes    int
	closed   bool
	stats    sessionEventsStats
	inflight inflightBatch
	dropLog  time.Time
	// dropLogged is stats.Dropped as of the last warning.
	dropLogged int64
	// rejectLog, rejectLogged and rejectStatus do the same for refusals.
	rejectLog     time.Time
	rejectLogged  int64
	rejectStatus  int
	rejectMessage string

	wake    chan struct{}
	closing chan struct{}
	done    chan struct{}

	closeOnce sync.Once

	// Test seams. Zero means the constant.
	flushEvery   time.Duration
	backoffMin   time.Duration
	backoffMax   time.Duration
	queueBytes   int
	queueEvents  int
	batchEvents  int
	closeTimeout time.Duration
	seqIdle      time.Duration
	seqSweep     time.Duration
}

// newSessionEventSink starts the sender. enabled is what the boot handshake
// said; the heartbeat moves it from there.
func newSessionEventSink(cp *controlPlane, opts audit.SinkOptions, enabled bool, log *slog.Logger) *sessionEventSink {
	s := newSessionEventSinkStopped(cp, opts, enabled, log)
	go s.run()
	return s
}

// newSessionEventSinkStopped builds the sink without its sender, so a test
// can set the seams before starting it.
func newSessionEventSinkStopped(cp *controlPlane, opts audit.SinkOptions, enabled bool, log *slog.Logger) *sessionEventSink {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	s := &sessionEventSink{
		cp:      cp,
		opts:    opts,
		log:     log,
		http:    controlPlaneHTTPClient(),
		seqs:    map[session.ID]seqCounter{},
		wake:    make(chan struct{}, 1),
		closing: make(chan struct{}),
		done:    make(chan struct{}),
	}
	s.enabled.Store(enabled)
	return s
}

func durationOr(v, def time.Duration) time.Duration {
	if v > 0 {
		return v
	}
	return def
}

func intOr(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}

// Write queues ev for the plane. It always returns nil; see the type.
func (s *sessionEventSink) Write(_ context.Context, ev audit.Event) error {
	// An event with no session has no seq to carry and no session to land
	// in. The gate always sets one; this guards a lane that does not.
	if ev.SessionID == "" {
		return nil
	}
	if !s.enabled.Load() {
		s.mu.Lock()
		s.number(ev)
		s.mu.Unlock()
		return nil
	}

	// The encoding happens outside the lock: it is the expensive part and
	// it needs nothing the lock guards. The seq is given under the lock, in
	// the same critical section that queues the entry, so two events of one
	// session queue in the order of their seq. The plane ignores an event
	// at or below the last seq it applied, so an inversion would lose one.
	raw, err := json.Marshal(s.opts.Apply(ev))
	if err != nil {
		s.mu.Lock()
		s.number(ev)
		s.stats.Dropped++
		s.mu.Unlock()
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	seq := s.number(ev)
	// Read again under the lock setEnabled empties the queue under, so a
	// write racing a turn-off does not leave an event behind it.
	if s.closed || !s.enabled.Load() {
		return nil
	}
	entry := make([]byte, 0, len(raw)+40)
	entry = append(entry, `{"seq":`...)
	entry = strconv.AppendInt(entry, seq, 10)
	entry = append(entry, `,"event":`...)
	entry = append(entry, raw...)
	entry = append(entry, '}')
	if len(entry) > maxSessionEventEntry {
		s.stats.Dropped++
		return nil
	}
	s.push(entry)
	if len(s.queue) >= s.batchLimit() || s.bytes >= maxSessionEventEntry {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
	return nil
}

// number gives ev its seq. Caller holds mu.
func (s *sessionEventSink) number(ev audit.Event) int64 {
	seq := s.seqs[ev.SessionID].seq + 1
	if ev.Kind == audit.KindSessionEnd {
		// The last event a session writes. An event after it would start
		// again at 1 and the plane would ignore it, which is the price of
		// not holding every session this process ever served.
		delete(s.seqs, ev.SessionID)
	} else {
		s.seqs[ev.SessionID] = seqCounter{seq: seq, at: time.Now()}
	}
	return seq
}

// sweepSeqs forgets the counter of every session idle past
// sessionEventsSeqIdle.
//
// A session whose session_end never reached this sink would otherwise hold
// its counter for the life of the process: an AsyncSink with a full queue
// refuses the session_end, and a lane can write an event after it. A session
// that writes again after that long starts over at 1, and the plane ignores
// what it sends; that is the price of the bound.
func (s *sessionEventSink) sweepSeqs(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now.Sub(s.lastSweep) < durationOr(s.seqSweep, sessionEventsSeqSweepEvery) {
		return
	}
	s.lastSweep = now
	idle := durationOr(s.seqIdle, sessionEventsSeqIdle)
	for id, c := range s.seqs {
		if now.Sub(c.at) > idle {
			delete(s.seqs, id)
		}
	}
}

// push appends entry, dropping the oldest events until it fits. Caller holds
// mu.
func (s *sessionEventSink) push(entry []byte) {
	maxBytes := intOr(s.queueBytes, sessionEventsQueueBytes)
	maxEvents := intOr(s.queueEvents, sessionEventsQueueEvents)
	for len(s.queue) > 0 && (len(s.queue)+1 > maxEvents || s.bytes+len(entry) > maxBytes) {
		s.dropOldest()
	}
	s.queue = append(s.queue, queuedEvent{pos: s.next, entry: entry})
	s.next++
	s.bytes += len(entry)
}

// dropOldest removes the head of the queue. Caller holds mu.
func (s *sessionEventSink) dropOldest() {
	s.bytes -= len(s.queue[0].entry)
	s.countDrop(s.queue[0].pos)
	// Cleared before the reslice, or the backing array keeps the bytes
	// alive until the next reallocation and the byte bound is a fiction.
	s.queue[0] = queuedEvent{}
	s.queue = s.queue[1:]
}

// countDrop counts an event that leaves the queue unsent. One in the batch on
// the wire is counted by that batch's answer instead: the plane may have
// taken it. Caller holds mu.
func (s *sessionEventSink) countDrop(pos uint64) {
	if s.inflight.active && pos >= s.inflight.first && pos <= s.inflight.last {
		s.inflight.evicted++
		return
	}
	s.stats.Dropped++
}

func (s *sessionEventSink) batchLimit() int {
	return min(intOr(s.batchEvents, MaxSessionEventsBatch), MaxSessionEventsBatch)
}

// setEnabled applies what a handshake said. Turning off empties the queue:
// the plane said it takes nothing, so holding events for it only holds
// memory, and turning on later starts from what happens then.
func (s *sessionEventSink) setEnabled(on bool) {
	if s.enabled.Swap(on) == on {
		return
	}
	if on {
		s.log.Info("the control plane takes session events; sending them", "url", s.cp.url)
		return
	}
	s.mu.Lock()
	discarded := len(s.queue)
	for _, q := range s.queue {
		s.countDrop(q.pos)
	}
	clear(s.queue)
	s.queue = s.queue[:0]
	s.bytes = 0
	s.mu.Unlock()
	s.log.Info("the control plane stopped taking session events; the local audit file keeps them",
		"url", s.cp.url, "discarded", discarded)
}

// snapshot copies the sink's counters for /stats.
func (s *sessionEventSink) snapshot() sessionEventsStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.stats
	out.Enabled = s.enabled.Load()
	out.Queued = len(s.queue)
	out.QueuedBytes = s.bytes
	return out
}

// take returns the body of the next batch and the pos of its last event, or
// a nil body when the queue is empty. The events stay queued until ack.
func (s *sessionEventSink) take() (body []byte, last uint64, n int) {
	s.mu.Lock()
	if len(s.queue) == 0 {
		s.mu.Unlock()
		return nil, 0, 0
	}
	size := len(sessionEventsBodyPrefix) + len(sessionEventsBodySuffix)
	limit := s.batchLimit()
	for n < len(s.queue) && n < limit {
		add := len(s.queue[n].entry)
		if n > 0 {
			add++ // the comma
		}
		if size+add > MaxSessionEventsBatchBytes {
			break
		}
		size += add
		n++
	}
	entries := make([][]byte, n)
	for i := range n {
		entries[i] = s.queue[i].entry
	}
	last = s.queue[n-1].pos
	s.inflight = inflightBatch{active: true, first: s.queue[0].pos, last: last}
	s.mu.Unlock()

	// Built outside the lock, which every Write takes: a batch is up to
	// 4 MiB. An entry never changes once queued, so the copy is safe.
	body = make([]byte, 0, size)
	body = append(body, sessionEventsBodyPrefix...)
	for i, e := range entries {
		if i > 0 {
			body = append(body, ',')
		}
		body = append(body, e...)
	}
	body = append(body, sessionEventsBodySuffix...)
	return body, last, n
}

// settle ends the batch in flight. It removes every queued event up to and
// including pos when the batch is final (sent or refused); events a full
// queue dropped while it was out are already gone, so it removes only what
// is still there. Then it counts the batch once, in exactly one place.
func (s *sessionEventSink) settle(pos uint64, n int, outcome batchOutcome) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if outcome != batchRetry {
		for len(s.queue) > 0 && s.queue[0].pos <= pos {
			s.bytes -= len(s.queue[0].entry)
			s.queue[0] = queuedEvent{}
			s.queue = s.queue[1:]
		}
	}
	switch outcome {
	case batchSent:
		s.stats.Sent += int64(n)
	case batchRefused:
		s.stats.Rejected += int64(n)
	case batchRetry:
		// What a full queue evicted while the batch was out will not be
		// sent again.
		s.stats.Retries++
		s.stats.Dropped += int64(s.inflight.evicted)
	}
	s.inflight = inflightBatch{}
}

type batchOutcome int

const (
	batchSent batchOutcome = iota
	batchRefused
	batchRetry
)

// run sends the queue until Close. One goroutine, one request at a time, so
// batches reach the plane in queue order.
func (s *sessionEventSink) run() {
	defer close(s.done)
	ticker := time.NewTicker(durationOr(s.flushEvery, sessionEventsFlushEvery))
	defer ticker.Stop()

	// Close does not cancel a request in flight: the plane may already have
	// applied it, and cancelling would only send it again. It cancels at
	// the close deadline, which bounds the request and the last flush
	// together.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-s.closing:
		case <-s.done:
			return
		}
		deadline := time.NewTimer(durationOr(s.closeTimeout, sessionEventsCloseTimeout))
		defer deadline.Stop()
		select {
		case <-deadline.C:
			cancel()
		case <-s.done:
		}
	}()

	backoff := time.Duration(0)
	for {
		select {
		case <-s.closing:
			s.finalFlush(ctx)
			return
		case <-ticker.C:
		case <-s.wake:
		}
		s.warnDropped()
		s.warnRejected(false)
		s.sweepSeqs(time.Now())

		for s.enabled.Load() {
			ok, retry := s.sendOne(ctx)
			if !retry {
				backoff = 0
			}
			if ok {
				continue
			}
			if !retry {
				break
			}
			// An outage is when the queue fills and drops, and this loop
			// does not leave until the plane answers.
			s.warnDropped()
			backoff = nextBackoff(backoff, durationOr(s.backoffMin, sessionEventsBackoffMin),
				durationOr(s.backoffMax, sessionEventsBackoffMax))
			select {
			case <-s.closing:
				s.finalFlush(ctx)
				return
			case <-time.After(backoff):
			}
		}
	}
}

// sendOne sends the next batch. ok reports a batch that left the queue
// (sent or rejected) so the caller sends the next one; retry reports a batch
// that stays for a resend. Both false means the queue is empty.
func (s *sessionEventSink) sendOne(ctx context.Context) (ok, retry bool) {
	body, last, n := s.take()
	if body == nil {
		return false, false
	}
	status, msg, err := s.post(ctx, body)
	switch {
	case err != nil || status >= 500:
		s.settle(last, n, batchRetry)
		if err == nil {
			err = fmt.Errorf("answered %d: %s", status, msg)
		}
		// A request Close cancelled is not a failure worth a line; the
		// last flush reports what it could not send.
		if ctx.Err() == nil {
			s.log.Warn("sending session events to the control plane failed; resending later",
				"url", s.cp.url, "events", n, "error", err)
		}
		return false, true
	case status >= 200 && status < 300:
		s.settle(last, n, batchSent)
		return true, false
	}

	// Every other answer is final: the same batch would get it again.
	s.settle(last, n, batchRefused)
	s.mu.Lock()
	s.rejectStatus, s.rejectMessage = status, msg
	s.mu.Unlock()
	s.warnRejected(false)
	if refusesEndpoint(status) {
		// Every batch would get the same answer, and a heartbeat that
		// fails the same way (a lapsed license, a deleted sidecar) would
		// never turn the sink off. A handshake that answers the header
		// turns it on again.
		s.setEnabled(false)
	}
	return true, false
}

// refusesEndpoint reports an answer about the route rather than the batch:
// the organization turned the feature off (412), the token or the license no
// longer holds (401, 403), or a replica does not have the route (404, 405).
func refusesEndpoint(status int) bool {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound,
		http.StatusMethodNotAllowed, http.StatusPreconditionFailed:
		return true
	}
	return false
}

// warnRejected logs the refusals since the last report, at most once per
// sessionEventsDropLogEvery; force skips the spacing, for the last word at
// shutdown. The first refusal is logged at once.
func (s *sessionEventSink) warnRejected(force bool) {
	s.mu.Lock()
	rejected := s.stats.Rejected
	since := rejected - s.rejectLogged
	due := since > 0 && (force || time.Since(s.rejectLog) >= sessionEventsDropLogEvery)
	status, msg := s.rejectStatus, s.rejectMessage
	if due {
		s.rejectLogged = rejected
		s.rejectLog = time.Now()
	}
	s.mu.Unlock()
	if due {
		s.log.Error("the control plane refused session events; they stay in the local audit file only",
			"url", s.cp.url, "status", status, "events", since, "rejected_total", rejected, "message", msg)
	}
}

// post sends one body. status and msg are the plane's answer when err is
// nil.
func (s *sessionEventSink) post(ctx context.Context, body []byte) (status int, msg string, err error) {
	// The base was validated by checkControlPlaneURL; JoinPath keeps a path
	// prefix (a plane behind /hoop) and normalizes trailing slashes.
	u, err := url.Parse(s.cp.url)
	if err != nil {
		return 0, "", fmt.Errorf("control plane URL %q: %w", s.cp.url, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		u.JoinPath(controlPlaneSessionEventsPath).String(), bytes.NewReader(body))
	if err != nil {
		return 0, "", fmt.Errorf("control plane request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(sidecarTokenHeader, s.cp.token)

	resp, err := s.http.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("the control plane at %s is unreachable: %w", s.cp.url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxSessionEventsResponse))
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		// Never followed, for the reason the handshake gives: the token
		// rides a custom header Go would forward across origins.
		return resp.StatusCode, fmt.Sprintf("redirected to %q; configure the final URL",
			resp.Header.Get("Location")), nil
	}
	return resp.StatusCode, controlPlaneMessage(raw), nil
}

// finalFlush tries each remaining batch once; ctx ends at the close
// deadline. A failure ends it: the process is stopping and the events are in
// the JSONL file.
func (s *sessionEventSink) finalFlush(ctx context.Context) {
	if !s.enabled.Load() {
		return
	}
	for {
		ok, _ := s.sendOne(ctx)
		if !ok || ctx.Err() != nil {
			break
		}
	}
	s.mu.Lock()
	left := len(s.queue)
	s.stats.Dropped += int64(left)
	s.mu.Unlock()
	if left > 0 {
		s.log.Warn("session events not sent at shutdown; the local audit file keeps them",
			"url", s.cp.url, "events", left)
	}
	s.warnDroppedNow(true)
	s.warnRejected(true)
}

// warnDropped logs the drop count when it grew, at most once per
// sessionEventsDropLogEvery.
func (s *sessionEventSink) warnDropped() { s.warnDroppedNow(false) }

// warnDroppedNow is warnDropped; force skips the spacing, for the last word
// at shutdown.
func (s *sessionEventSink) warnDroppedNow(force bool) {
	s.mu.Lock()
	dropped := s.stats.Dropped
	due := dropped > s.dropLogged && (force || time.Since(s.dropLog) >= sessionEventsDropLogEvery)
	if due {
		s.dropLogged = dropped
		s.dropLog = time.Now()
	}
	s.mu.Unlock()
	if due {
		s.log.Warn("session events were dropped before reaching the control plane; "+
			"the local audit file keeps them",
			"url", s.cp.url, "dropped_total", dropped)
	}
}

// Close sends what is queued, within sessionEventsCloseTimeout, and stops the
// sender. Safe to call twice. Writes after it are discarded.
func (s *sessionEventSink) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		close(s.closing)
		<-s.done
	})
	return nil
}

// nextBackoff doubles the wait up to the ceiling, with a jitter so a fleet
// that lost its gateway at once does not come back at once.
func nextBackoff(prev, lo, hi time.Duration) time.Duration {
	next := lo
	if prev > 0 {
		next = min(prev*2, hi)
	}
	jitter := time.Duration(rand.Int64N(int64(next)/5 + 1))
	return next - next/10 + jitter
}

var _ audit.Sink = (*sessionEventSink)(nil)
