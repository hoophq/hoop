package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/session"
)

// eventsPlane is a fake control plane for POST /api/sidecars/events. It
// records every batch it was sent, answers from a script of statuses and then
// 200, and can hold requests until a test releases them.
type eventsPlane struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	arrived  int
	batches  [][]SessionEvent
	statuses []int
	tokens   []string
	paths    []string

	// hold, when set, makes the handler wait for it to close.
	hold chan struct{}
	// maxBody, when set, answers 413 to a larger body, as a proxy in front
	// of the plane does. The plane never sees it.
	maxBody int
	// cut is how many answers end mid-body, as a dropped connection does.
	cut int
}

func newEventsPlane(t *testing.T, statuses ...int) *eventsPlane {
	t.Helper()
	p := &eventsPlane{t: t, statuses: statuses}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		hold := p.hold
		p.arrived++
		p.mu.Unlock()
		if hold != nil {
			<-hold
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading the body: %v", err)
		}
		if p.maxBody > 0 && len(raw) > p.maxBody {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		var req SessionEventsRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Errorf("the sink sent a body the plane cannot decode: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		p.mu.Lock()
		p.batches = append(p.batches, req.Events)
		p.tokens = append(p.tokens, r.Header.Get(sidecarTokenHeader))
		p.paths = append(p.paths, r.URL.Path)
		status := http.StatusOK
		if len(p.statuses) > 0 {
			status, p.statuses = p.statuses[0], p.statuses[1:]
		}
		cut := p.cut > 0
		if cut {
			p.cut--
		}
		p.mu.Unlock()
		if cut {
			// Fewer bytes than declared: the server drops the connection.
			w.Header().Set("Content-Length", "100")
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"mess`)
			return
		}
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `{"message":"status %d"}`, status)
	}))
	t.Cleanup(p.srv.Close)
	return p
}

// received returns every batch so far, copied.
func (p *eventsPlane) received() [][]SessionEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([][]SessionEvent, len(p.batches))
	copy(out, p.batches)
	return out
}

// applied flattens the batches the plane answered 2xx, which is what a real
// plane would hold. With a script of statuses the caller says which to skip.
func flatten(batches [][]SessionEvent) []SessionEvent {
	var out []SessionEvent
	for _, b := range batches {
		out = append(out, b...)
	}
	return out
}

// testSink builds a sink against plane with test-sized timings. tweak runs
// before the sender starts.
func testSink(t *testing.T, plane *eventsPlane, enabled bool, opts audit.SinkOptions,
	tweak func(*sessionEventSink)) (*sessionEventSink, *syncBuffer) {
	t.Helper()
	buf := &syncBuffer{}
	cp := &controlPlane{url: plane.srv.URL, cred: tokenCredential("hsc_events")}
	s := newSessionEventSinkStopped(cp, opts, enabled, slog.New(slog.NewTextHandler(buf, nil)))
	s.flushEvery = time.Hour
	s.backoffMin = time.Millisecond
	s.backoffMax = 5 * time.Millisecond
	s.closeTimeout = 2 * time.Second
	if tweak != nil {
		tweak(s)
	}
	go s.run()
	t.Cleanup(func() { _ = s.Close() })
	return s, buf
}

func statementEvent(sid session.ID, text string) audit.Event {
	return audit.Event{
		Kind:       audit.KindStatement,
		Timestamp:  time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC),
		SessionID:  sid,
		Principal:  "alice@example.com",
		Protocol:   "postgres",
		Connection: "appdb",
		Statement:  text,
		Allowed:    true,
	}
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// A full batch goes at once, without waiting for the timer, and carries no
// more than the batch limit.
func TestSessionEventsSendAFullBatchAtOnce(t *testing.T) {
	plane := newEventsPlane(t)
	s, _ := testSink(t, plane, true, audit.SinkOptions{}, func(s *sessionEventSink) {
		s.batchEvents = 3
	})
	for i := range 7 {
		if err := s.Write(context.Background(), statementEvent("s1", fmt.Sprintf("select %d", i))); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	// A full batch wakes the sender; the timer is an hour away, so whatever
	// a batch left behind goes with Close, which flushes.
	waitUntil(t, "a full batch", func() bool { return len(flatten(plane.received())) >= 3 })
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	for i, b := range plane.received() {
		if len(b) > 3 {
			t.Errorf("batch %d carries %d events, over the limit of 3", i, len(b))
		}
	}
	got := flatten(plane.received())
	if len(got) != 7 {
		t.Fatalf("the plane received %d events, want 7: %+v", len(got), plane.received())
	}
	for i, ev := range got {
		if ev.Seq != int64(i+1) || ev.Event.Statement != fmt.Sprintf("select %d", i) {
			t.Errorf("event %d = seq %d %q, want seq %d in write order", i, ev.Seq, ev.Event.Statement, i+1)
		}
	}
	plane.mu.Lock()
	defer plane.mu.Unlock()
	for i := range plane.tokens {
		if plane.tokens[i] != "hsc_events" || plane.paths[i] != controlPlaneSessionEventsPath {
			t.Errorf("request %d went to %q with token %q", i, plane.paths[i], plane.tokens[i])
		}
	}
}

// A batch that never fills goes when the timer fires.
func TestSessionEventsSendAPartialBatchOnTheTimer(t *testing.T) {
	plane := newEventsPlane(t)
	s, _ := testSink(t, plane, true, audit.SinkOptions{}, func(s *sessionEventSink) {
		s.flushEvery = 5 * time.Millisecond
	})
	_ = s.Write(context.Background(), statementEvent("s1", "select 1"))
	_ = s.Write(context.Background(), statementEvent("s1", "select 2"))
	waitUntil(t, "the timer's batch", func() bool { return len(flatten(plane.received())) == 2 })
}

// Each session numbers its own events from 1, interleaved or not, and a
// session that ends gives its counter back.
func TestSessionEventsNumberEachSessionFromOne(t *testing.T) {
	plane := newEventsPlane(t)
	s, _ := testSink(t, plane, true, audit.SinkOptions{}, nil)
	ctx := context.Background()
	_ = s.Write(ctx, audit.Event{Kind: audit.KindSessionStart, SessionID: "a"})
	_ = s.Write(ctx, audit.Event{Kind: audit.KindSessionStart, SessionID: "b"})
	_ = s.Write(ctx, statementEvent("a", "select 1"))
	_ = s.Write(ctx, statementEvent("b", "select 2"))
	_ = s.Write(ctx, statementEvent("a", "select 3"))
	_ = s.Write(ctx, audit.Event{Kind: audit.KindSessionEnd, SessionID: "a"})
	// An event with no session cannot be numbered and is not sent.
	_ = s.Write(ctx, audit.Event{Kind: audit.KindError})
	_ = s.Close()

	want := map[session.ID][]int64{"a": {1, 2, 3, 4}, "b": {1, 2}}
	got := map[session.ID][]int64{}
	for _, ev := range flatten(plane.received()) {
		got[ev.Event.SessionID] = append(got[ev.Event.SessionID], ev.Seq)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("seqs = %v, want %v", got, want)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, open := s.seqs["a"]; open {
		t.Error("an ended session still holds its counter")
	}
}

// A plane that fails gets the same batch again, with the same seqs, until it
// answers. The plane ignores what it already applied, so the resend is safe.
func TestSessionEventsResendTheSameSeqAfterA5xx(t *testing.T) {
	plane := newEventsPlane(t, http.StatusInternalServerError, http.StatusBadGateway)
	// The second write fills the batch and wakes the sender, so both events
	// are queued before the first attempt.
	s, buf := testSink(t, plane, true, audit.SinkOptions{}, func(s *sessionEventSink) {
		s.batchEvents = 2
	})
	_ = s.Write(context.Background(), statementEvent("s1", "select 1"))
	_ = s.Write(context.Background(), statementEvent("s1", "select 2"))
	waitUntil(t, "three attempts", func() bool { return len(plane.received()) >= 3 })

	batches := plane.received()
	for i, b := range batches[:3] {
		if len(b) != 2 || b[0].Seq != 1 || b[1].Seq != 2 {
			t.Errorf("attempt %d = %+v, want seqs 1 and 2", i, b)
		}
	}
	waitUntil(t, "the queue to empty", func() bool { return s.snapshot().Queued == 0 })
	st := s.snapshot()
	if st.Sent != 2 || st.Retries != 2 || st.Dropped != 0 || st.Rejected != 0 {
		t.Errorf("stats = %+v, want 2 sent after 2 retries", st)
	}
	if !strings.Contains(buf.String(), "resending later") {
		t.Errorf("a failed send was not logged: %s", buf.String())
	}
}

// An answer cut mid-body is a transport failure, whatever its status: the
// batch goes again with the same seqs.
func TestSessionEventsResendAfterACutAnswer(t *testing.T) {
	plane := newEventsPlane(t)
	plane.cut = 1
	s, buf := testSink(t, plane, true, audit.SinkOptions{}, func(s *sessionEventSink) {
		s.batchEvents = 2
	})
	_ = s.Write(context.Background(), statementEvent("s1", "select 1"))
	_ = s.Write(context.Background(), statementEvent("s1", "select 2"))
	waitUntil(t, "the queue to empty", func() bool { return s.snapshot().Sent == 2 })

	batches := plane.received()
	if len(batches) != 2 {
		t.Fatalf("the plane received %d batches, want the cut one and its resend: %+v", len(batches), batches)
	}
	for i, b := range batches {
		if len(b) != 2 || b[0].Seq != 1 || b[1].Seq != 2 {
			t.Errorf("attempt %d = %+v, want seqs 1 and 2", i, b)
		}
	}
	if st := s.snapshot(); st.Retries != 1 || st.Rejected != 0 || st.Dropped != 0 {
		t.Errorf("stats = %+v, want 2 sent after 1 retry", st)
	}
	if !strings.Contains(buf.String(), "reading the answer") {
		t.Errorf("the cut answer was not logged: %s", buf.String())
	}
}

// A 4xx is final: the batch leaves the queue and is counted, and nothing is
// resent.
func TestSessionEventsNeverResendAfterA4xx(t *testing.T) {
	plane := newEventsPlane(t, http.StatusBadRequest)
	s, buf := testSink(t, plane, true, audit.SinkOptions{}, func(s *sessionEventSink) {
		s.flushEvery = time.Millisecond
	})
	_ = s.Write(context.Background(), statementEvent("s1", "select 1"))
	waitUntil(t, "the rejection", func() bool { return s.snapshot().Rejected == 1 })
	_ = s.Write(context.Background(), statementEvent("s1", "select 2"))
	waitUntil(t, "the next event", func() bool { return s.snapshot().Sent == 1 })

	got := flatten(plane.received())
	if len(got) != 2 || got[0].Seq != 1 || got[1].Seq != 2 {
		t.Errorf("the plane received %+v, want seq 1 once, then seq 2", got)
	}
	if !strings.Contains(buf.String(), "refused session events") {
		t.Errorf("the rejection was not logged: %s", buf.String())
	}
}

// 412 is the organization turning the feature off. The sink stops at once
// rather than at the next heartbeat.
func TestSessionEventsStopOnA412(t *testing.T) {
	plane := newEventsPlane(t, http.StatusPreconditionFailed)
	s, _ := testSink(t, plane, true, audit.SinkOptions{}, func(s *sessionEventSink) {
		s.flushEvery = time.Millisecond
	})
	_ = s.Write(context.Background(), statementEvent("s1", "select 1"))
	waitUntil(t, "the sink to stop", func() bool { return !s.snapshot().Enabled })
	_ = s.Write(context.Background(), statementEvent("s1", "select 2"))
	time.Sleep(20 * time.Millisecond)
	if n := len(plane.received()); n != 1 {
		t.Errorf("the plane received %d requests after a 412, want 1", n)
	}
}

// A plane that does not answer must not reach the user's statement: Write
// returns at once, the queue holds no more than its bound, the oldest events
// go first and every drop is counted. What arrives afterwards is in order,
// with gaps where the drops were.
func TestSessionEventsDropTheOldestUnderPressureWithoutBlocking(t *testing.T) {
	plane := newEventsPlane(t)
	release := make(chan struct{})
	plane.hold = release
	s, buf := testSink(t, plane, true, audit.SinkOptions{}, func(s *sessionEventSink) {
		s.flushEvery = time.Millisecond
		s.queueEvents = 5
		s.batchEvents = 2
	})

	// The first batch goes out and hangs in the plane.
	_ = s.Write(context.Background(), statementEvent("s1", "select 0"))
	_ = s.Write(context.Background(), statementEvent("s1", "select 1"))
	waitUntil(t, "a request in flight", func() bool {
		plane.mu.Lock()
		defer plane.mu.Unlock()
		return plane.arrived == 1
	})

	start := time.Now()
	for i := 2; i < 1000; i++ {
		if err := s.Write(context.Background(), statementEvent("s1", fmt.Sprintf("select %d", i))); err != nil {
			t.Fatalf("Write returned an error under pressure: %v", err)
		}
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("1000 writes against a stuck plane took %s; Write blocked", took)
	}
	st := s.snapshot()
	if st.Queued > 5 {
		t.Errorf("the queue holds %d events, over its bound of 5", st.Queued)
	}
	if st.Dropped < 990 {
		t.Errorf("dropped = %d, want every event past the bound counted", st.Dropped)
	}

	close(release)
	waitUntil(t, "the queue to drain", func() bool { return s.snapshot().Queued == 0 })
	_ = s.Close()

	// The batch that hung, then exactly the five the queue held: an ack
	// that removed by count instead of by pos would cut into these.
	var seqs []int64
	for _, ev := range flatten(plane.received()) {
		seqs = append(seqs, ev.Seq)
	}
	if want := []int64{1, 2, 996, 997, 998, 999, 1000}; fmt.Sprint(seqs) != fmt.Sprint(want) {
		t.Errorf("the plane received seqs %v, want %v", seqs, want)
	}
	// Every event counted once. The two in flight were evicted from the
	// queue, but the plane took them, so they are sent and not dropped.
	if st := s.snapshot(); st.Sent != 7 || st.Dropped != 993 {
		t.Errorf("stats = %+v, want 7 sent and 993 dropped", st)
	}
	if !strings.Contains(buf.String(), "dropped") {
		t.Errorf("the drops were not logged: %s", buf.String())
	}
}

// The queue bound is in bytes as well as events.
func TestSessionEventsBoundTheQueueInBytes(t *testing.T) {
	plane := newEventsPlane(t)
	s, _ := testSink(t, plane, false, audit.SinkOptions{}, func(s *sessionEventSink) {
		s.queueBytes = 1000
	})
	s.enabled.Store(true) // without the sender noticing: nothing is sent
	for i := range 50 {
		_ = s.Write(context.Background(), statementEvent("s1", fmt.Sprintf("select %d", i)))
	}
	st := s.snapshot()
	if st.QueuedBytes > 1000 || st.Dropped == 0 {
		t.Errorf("queued %d bytes with %d dropped; want at most 1000 bytes", st.QueuedBytes, st.Dropped)
	}
}

// An event too large for any batch is dropped and counted, never sent to be
// refused with a 413.
func TestSessionEventsDropAnEventNoBatchCanCarry(t *testing.T) {
	plane := newEventsPlane(t)
	s, _ := testSink(t, plane, true, audit.SinkOptions{MaxStatementBytes: MaxSessionEventsBatchBytes * 2}, nil)
	_ = s.Write(context.Background(), statementEvent("s1", strings.Repeat("x", MaxSessionEventsBatchBytes)))
	_ = s.Write(context.Background(), statementEvent("s1", "select 1"))
	_ = s.Close()
	got := flatten(plane.received())
	if len(got) != 1 || got[0].Seq != 2 {
		t.Errorf("the plane received %+v, want only seq 2", got)
	}
	if st := s.snapshot(); st.Dropped != 1 {
		t.Errorf("dropped = %d, want 1", st.Dropped)
	}
}

// The plane gets what the JSONL file gets: the same redaction and the same
// statement cap, applied by the same options.
func TestSessionEventsApplyTheSinkOptions(t *testing.T) {
	plane := newEventsPlane(t)
	redact, _ := testSink(t, plane, true, audit.SinkOptions{RedactStatements: true}, nil)
	_ = redact.Write(context.Background(), statementEvent("s1", "select ssn from people"))
	_ = redact.Close()

	capped, _ := testSink(t, plane, true, audit.SinkOptions{MaxStatementBytes: 8}, nil)
	_ = capped.Write(context.Background(), statementEvent("s2", "select ssn from people"))
	_ = capped.Close()

	got := flatten(plane.received())
	if len(got) != 2 {
		t.Fatalf("the plane received %d events, want 2", len(got))
	}
	if st := got[0].Event.Statement; !strings.HasPrefix(st, "sha256:") {
		t.Errorf("redacted statement = %q, want a fingerprint", st)
	}
	if st := got[1].Event.Statement; st != "select s...[truncated]" {
		t.Errorf("capped statement = %q", st)
	}
}

// A sink the plane never enabled sends nothing, and its writes still
// succeed. It keeps numbering: a session that was open when the plane turned
// the feature on continues from its count, not from 1.
func TestSessionEventsSendNothingUntilEnabled(t *testing.T) {
	plane := newEventsPlane(t)
	s, _ := testSink(t, plane, false, audit.SinkOptions{}, func(s *sessionEventSink) {
		s.flushEvery = time.Millisecond
	})
	for i := range 3 {
		if err := s.Write(context.Background(), statementEvent("s1", fmt.Sprintf("select %d", i))); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	time.Sleep(20 * time.Millisecond)
	if n := len(plane.received()); n != 0 {
		t.Fatalf("a disabled sink sent %d requests", n)
	}

	s.setEnabled(true)
	_ = s.Write(context.Background(), statementEvent("s1", "select 3"))
	waitUntil(t, "the first event after enabling", func() bool { return len(flatten(plane.received())) == 1 })
	if got := flatten(plane.received())[0]; got.Seq != 4 {
		t.Errorf("seq after enabling = %d, want 4", got.Seq)
	}
}

// Turning off empties the queue: the plane said it takes nothing.
func TestSessionEventsTurningOffEmptiesTheQueue(t *testing.T) {
	plane := newEventsPlane(t)
	s, buf := testSink(t, plane, true, audit.SinkOptions{}, nil)
	for i := range 4 {
		_ = s.Write(context.Background(), statementEvent("s1", fmt.Sprintf("select %d", i)))
	}
	s.setEnabled(false)
	st := s.snapshot()
	if st.Queued != 0 || st.QueuedBytes != 0 || st.Dropped != 4 {
		t.Errorf("stats after turning off = %+v, want an empty queue and 4 dropped", st)
	}
	_ = s.Close()
	if n := len(plane.received()); n != 0 {
		t.Errorf("the plane received %d requests after the sink turned off", n)
	}
	if !strings.Contains(buf.String(), "stopped taking session events") {
		t.Errorf("turning off was not logged: %s", buf.String())
	}
}

// After Close a write is discarded without an error: the gate must never
// refuse a statement over this sink.
func TestSessionEventsWriteAfterCloseSucceeds(t *testing.T) {
	plane := newEventsPlane(t)
	s, _ := testSink(t, plane, true, audit.SinkOptions{}, nil)
	_ = s.Close()
	if err := s.Write(context.Background(), statementEvent("s1", "select 1")); err != nil {
		t.Errorf("Write after Close = %v, want nil", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("second Close = %v", err)
	}
}

// The handshake answer carries the header, and the boot handshake puts it on
// the connection the sink starts from.
func TestTheHandshakeReadsTheSessionEventsHeader(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header string
		want   bool
	}{
		{"a plane that takes events", "true", true},
		{"a plane that does not say", "", false},
		{"a plane that says anything else", "yes", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.header != "" {
					w.Header().Set(SessionEventsHeader, tc.header)
				}
				w.Header().Set(LicenseManagedHeader, "true")
				_, _ = w.Write([]byte(planeConfig))
			}))
			defer srv.Close()
			t.Setenv(ControlPlaneURLEnv, srv.URL)
			t.Setenv(SidecarTokenEnv, "hsc_x")
			cfg, _, err := SetupWith("", nil, nil)
			if err != nil {
				t.Fatalf("SetupWith: %v", err)
			}
			if cfg.cp.sessionEvents != tc.want {
				t.Errorf("sessionEvents = %v, want %v", cfg.cp.sessionEvents, tc.want)
			}
		})
	}
}

// The heartbeat re-reads the header on every answer: the sink starts when
// the plane starts saying it and stops when the plane stops. A failed
// handshake changes nothing.
func TestTheHeartbeatTurnsTheSinkOnAndOff(t *testing.T) {
	rl, _ := testReloader(t, reloadBase)
	plane := newEventsPlane(t)
	sink, sinkLog := testSink(t, plane, false, audit.SinkOptions{}, nil)

	var mu sync.Mutex
	answers := []string{"true", "fail", "true", ""}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		answer := answers[min(calls, len(answers)-1)]
		calls++
		if answer == "fail" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if answer != "" {
			w.Header().Set(SessionEventsHeader, answer)
		}
		w.Header().Set(LicenseManagedHeader, "true")
		_, _ = w.Write([]byte(reloadBase))
	}))
	defer srv.Close()

	cp := &controlPlane{url: srv.URL, cred: tokenCredential("hsc_x"), every: time.Millisecond, events: sink}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		cp.heartbeat(ctx, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), rl)
		close(done)
	}()
	waitUntil(t, "five handshakes", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return calls >= 5
	})
	cancel()
	<-done

	if sink.enabled.Load() {
		t.Error("the sink is on after the plane stopped sending the header")
	}
	// Each transition logs once, so the log is the sequence of states. The
	// failed handshake between the two "true" answers must not have turned
	// the sink off and on again.
	logged := sinkLog.String()
	on := strings.Count(logged, "the control plane takes session events")
	off := strings.Count(logged, "stopped taking session events")
	if on != 1 || off != 1 || strings.Index(logged, "takes session events") > strings.Index(logged, "stopped taking") {
		t.Errorf("want one turn on, then one turn off; the sink logged:\n%s", logged)
	}
}

// The sidecar says it can send events, so the plane can tell a build that
// will from one that cannot.
func TestTheCapabilitiesNameSessionEvents(t *testing.T) {
	found := false
	for _, c := range SidecarCapabilities() {
		if c == CapabilitySessionEvents {
			found = true
		}
	}
	if !found {
		t.Errorf("the header lacks %q: %v", CapabilitySessionEvents, SidecarCapabilities())
	}
}

// buildAudit adds the sink only for a process with a plane, and an event
// written to the chain reaches it.
func TestBuildAuditAddsTheSinkOnlyWithAPlane(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	file := filepath.Join(t.TempDir(), "audit.jsonl")
	standalone, err := buildAudit(AuditConfig{File: file}, nil, log)
	if err != nil {
		t.Fatalf("buildAudit: %v", err)
	}
	defer func() { _ = standalone.sink.Close() }()
	if standalone.events != nil {
		t.Error("a standalone process built the session events sink")
	}

	plane := newEventsPlane(t)
	cp := &controlPlane{url: plane.srv.URL, cred: tokenCredential("hsc_x"), sessionEvents: true}
	chain, err := buildAudit(AuditConfig{File: file, RedactStatements: true}, cp, log)
	if err != nil {
		t.Fatalf("buildAudit: %v", err)
	}
	if chain.events == nil {
		t.Fatal("a plane-connected process has no session events sink")
	}
	_ = chain.sink.Write(context.Background(), statementEvent("s1", "select 1"))
	if err := chain.sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	got := flatten(plane.received())
	if len(got) != 1 || !strings.HasPrefix(got[0].Event.Statement, "sha256:") {
		t.Errorf("the plane received %+v, want one redacted statement", got)
	}
}

// A batch never carries more than the byte limit: large events split across
// requests, and every one arrives.
func TestSessionEventsSplitABatchAtTheByteLimit(t *testing.T) {
	plane := newEventsPlane(t)
	s, _ := testSink(t, plane, true, audit.SinkOptions{MaxStatementBytes: 2 << 20}, nil)
	const n = 5
	for i := range n {
		_ = s.Write(context.Background(), statementEvent("s1", fmt.Sprintf("%d%s", i, strings.Repeat("x", 1<<20))))
	}
	_ = s.Close()

	plane.mu.Lock()
	batches := len(plane.batches)
	plane.mu.Unlock()
	if batches < 2 {
		t.Errorf("five 1 MiB events went in %d request(s); the limit is %d bytes", batches, MaxSessionEventsBatchBytes)
	}
	for i, b := range plane.received() {
		raw, err := json.Marshal(SessionEventsRequest{Events: b})
		if err != nil {
			t.Fatal(err)
		}
		if len(raw) > MaxSessionEventsBatchBytes {
			t.Errorf("request %d is %d bytes, over the limit", i, len(raw))
		}
	}
	if got := flatten(plane.received()); len(got) != n {
		t.Errorf("the plane received %d events, want %d", len(got), n)
	}
}

// A plane that never answers does not hold shutdown past the close deadline,
// and what it never took is counted as dropped.
func TestSessionEventsCloseReturnsAtTheDeadline(t *testing.T) {
	plane := newEventsPlane(t)
	plane.hold = make(chan struct{})
	t.Cleanup(func() { close(plane.hold) })
	s, _ := testSink(t, plane, true, audit.SinkOptions{}, func(s *sessionEventSink) {
		s.flushEvery = time.Millisecond
		s.closeTimeout = 100 * time.Millisecond
	})
	_ = s.Write(context.Background(), statementEvent("s1", "select 1"))
	waitUntil(t, "a request in flight", func() bool {
		plane.mu.Lock()
		defer plane.mu.Unlock()
		return plane.arrived == 1
	})
	_ = s.Write(context.Background(), statementEvent("s1", "select 2"))

	start := time.Now()
	_ = s.Close()
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("Close took %s against a plane that never answers; the deadline is 100ms", took)
	}
	if st := s.snapshot(); st.Dropped != 2 || st.Sent != 0 {
		t.Errorf("stats = %+v, want both events dropped", st)
	}
}

// A refusal of the route itself (a lapsed license, a deleted sidecar, an old
// replica) stops the sink like a 412: a heartbeat failing the same way would
// never turn it off. A refusal of one batch does not.
func TestSessionEventsStopOnARefusedRoute(t *testing.T) {
	for _, tc := range []struct {
		status int
		stops  bool
	}{
		{http.StatusUnauthorized, true},
		{http.StatusForbidden, true},
		{http.StatusNotFound, true},
		{http.StatusPreconditionFailed, true},
		{http.StatusBadRequest, false},
		{http.StatusUnprocessableEntity, false},
		{http.StatusRequestEntityTooLarge, false},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			plane := newEventsPlane(t, tc.status)
			s, _ := testSink(t, plane, true, audit.SinkOptions{}, func(s *sessionEventSink) {
				s.flushEvery = time.Millisecond
			})
			_ = s.Write(context.Background(), statementEvent("s1", "select 1"))
			if tc.stops {
				waitUntil(t, "the sink to stop", func() bool { return !s.snapshot().Enabled })
				return
			}
			waitUntil(t, "the refusal", func() bool { return s.snapshot().Rejected == 1 })
			time.Sleep(20 * time.Millisecond)
			if !s.snapshot().Enabled {
				t.Errorf("a %d for one batch stopped the sink", tc.status)
			}
		})
	}
}

// A plane that refuses every batch logs once, not once per batch.
func TestSessionEventsSpaceTheRefusalLog(t *testing.T) {
	statuses := make([]int, 50)
	for i := range statuses {
		statuses[i] = http.StatusUnprocessableEntity
	}
	plane := newEventsPlane(t, statuses...)
	s, buf := testSink(t, plane, true, audit.SinkOptions{}, func(s *sessionEventSink) {
		s.batchEvents = 1
	})
	for i := range 50 {
		_ = s.Write(context.Background(), statementEvent("s1", fmt.Sprintf("select %d", i)))
	}
	waitUntil(t, "every refusal", func() bool { return s.snapshot().Rejected == 50 })
	if n := strings.Count(buf.String(), "refused session events"); n != 1 {
		t.Errorf("50 refusals logged %d lines, want 1 until the minute passes", n)
	}
	// Shutdown reports what the spacing held back.
	_ = s.Close()
	if n := strings.Count(buf.String(), "refused session events"); n != 2 {
		t.Errorf("Close logged no final count:\n%s", buf.String())
	}
}

// A counter whose session_end never came is forgotten once idle, so the map
// holds the sessions in flight and not every session ever served.
func TestSessionEventsForgetAnIdleCounter(t *testing.T) {
	plane := newEventsPlane(t)
	s, _ := testSink(t, plane, false, audit.SinkOptions{}, func(s *sessionEventSink) {
		s.seqIdle = time.Hour
		s.seqSweep = time.Nanosecond
	})
	_ = s.Write(context.Background(), statementEvent("old", "select 1"))
	_ = s.Write(context.Background(), statementEvent("new", "select 1"))
	s.mu.Lock()
	s.seqs["old"] = seqCounter{seq: 7, at: time.Now().Add(-2 * time.Hour)}
	s.mu.Unlock()

	s.sweepSeqs(time.Now())
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.seqs["old"]; ok {
		t.Error("an idle counter survived the sweep")
	}
	if c, ok := s.seqs["new"]; !ok || c.seq != 1 {
		t.Errorf("a live counter = %+v, %v; want seq 1 kept", c, ok)
	}
}

// A proxy that allows less than the plane does answers 413 to a batch the
// plane would take. The sink halves its batches until they pass, and loses
// only an event too large to pass alone.
func TestSessionEventsShrinkTheBatchOnAProxy413(t *testing.T) {
	plane := newEventsPlane(t)
	plane.maxBody = 1000
	s, buf := testSink(t, plane, true, audit.SinkOptions{}, nil)
	for i := range 10 {
		_ = s.Write(context.Background(), statementEvent("s1", fmt.Sprintf("select %d", i)))
	}
	_ = s.Write(context.Background(), statementEvent("s1", strings.Repeat("x", 2000)))
	_ = s.Write(context.Background(), statementEvent("s1", "select 11"))
	_ = s.Close()

	var seqs []int64
	for _, ev := range flatten(plane.received()) {
		seqs = append(seqs, ev.Seq)
	}
	want := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 12}
	if fmt.Sprint(seqs) != fmt.Sprint(want) {
		t.Errorf("the plane received seqs %v, want %v: all but the event too large alone", seqs, want)
	}
	if st := s.snapshot(); st.Sent != 11 || st.Rejected != 1 || st.Dropped != 0 {
		t.Errorf("stats = %+v, want 11 sent and the large one rejected", st)
	}
	if !strings.Contains(buf.String(), "sending smaller ones") {
		t.Errorf("the shrink was not logged:\n%s", buf.String())
	}
}
