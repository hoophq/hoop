package audit

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/inspect"
)

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

// decodeLines parses JSONL output, failing the test on any malformed line.
func decodeLines(t *testing.T, raw string) []Event {
	t.Helper()
	var out []Event
	for i, line := range strings.Split(strings.TrimRight(raw, "\n"), "\n") {
		if line == "" {
			continue
		}
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("line %d is not valid JSON (%v): %q", i, err, line)
		}
		out = append(out, ev)
	}
	return out
}

func TestJSONLSinkWritesOneObjectPerLine(t *testing.T) {
	var buf bytes.Buffer
	s := NewJSONLSink(&buf, SinkOptions{Now: fixedClock(time.Unix(1700000000, 0).UTC())})

	for _, stmt := range []string{"SELECT 1", "DELETE FROM users"} {
		if err := s.Write(context.Background(), Event{Kind: KindStatement, Statement: stmt}); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	got := decodeLines(t, buf.String())
	if len(got) != 2 {
		t.Fatalf("got %d lines, want 2", len(got))
	}
	if got[1].Statement != "DELETE FROM users" {
		t.Errorf("Statement = %q", got[1].Statement)
	}
	if !got[0].Timestamp.Equal(time.Unix(1700000000, 0).UTC()) {
		t.Errorf("zero Timestamp was not stamped from the injected clock: %v", got[0].Timestamp)
	}
	if !strings.HasSuffix(buf.String(), "\n") {
		t.Error("output does not end in a newline; a reader tailing the file would block on the last record")
	}
}

func TestJSONLSinkPreservesSuppliedTimestampAsUTC(t *testing.T) {
	var buf bytes.Buffer
	s := NewJSONLSink(&buf, SinkOptions{Now: fixedClock(time.Unix(0, 0))})

	zone := time.FixedZone("UTC+7", 7*3600)
	ts := time.Date(2026, 7, 28, 9, 0, 0, 0, zone)
	if err := s.Write(context.Background(), Event{Kind: KindStatement, Timestamp: ts}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got := decodeLines(t, buf.String())[0]
	if !got.Timestamp.Equal(ts) {
		t.Errorf("Timestamp = %v, want the same instant as %v", got.Timestamp, ts)
	}
	if _, offset := got.Timestamp.Zone(); offset != 0 {
		t.Errorf("Timestamp offset = %d, want UTC so lines from several hosts sort lexicographically", offset)
	}
}

func TestJSONLSinkDoesNotEscapeSQLOperators(t *testing.T) {
	var buf bytes.Buffer
	s := NewJSONLSink(&buf, SinkOptions{})
	if err := s.Write(context.Background(), Event{Statement: "SELECT * FROM t WHERE age > 30 AND x < 5"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if strings.Contains(buf.String(), `\u003e`) {
		t.Errorf("comparison operators were HTML-escaped, breaking grep: %s", buf.String())
	}
}

func TestJSONLSinkConcurrentWritesProduceIntactLines(t *testing.T) {
	const goroutines, perGoroutine = 16, 64

	var buf bytes.Buffer
	s := NewJSONLSink(&buf, SinkOptions{Now: fixedClock(time.Unix(1, 0).UTC())})

	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range perGoroutine {
				ev := Event{
					Kind: KindStatement,
					// A long statement widens the window for a torn write.
					Statement: strings.Repeat("x", 300),
					Principal: "user",
					Rule:      "g",
					Message:   string(rune('a'+g%26)) + ":" + string(rune('a'+i%26)),
				}
				if err := s.Write(context.Background(), ev); err != nil {
					t.Errorf("Write: %v", err)
				}
			}
		}()
	}
	wg.Wait()

	got := decodeLines(t, buf.String())
	if len(got) != goroutines*perGoroutine {
		t.Fatalf("got %d lines, want %d", len(got), goroutines*perGoroutine)
	}
	for i, ev := range got {
		if len(ev.Statement) != 300 {
			t.Fatalf("line %d has a torn statement of %d bytes", i, len(ev.Statement))
		}
	}
}

func TestJSONLSinkWriteAfterCloseErrors(t *testing.T) {
	var buf bytes.Buffer
	s := NewJSONLSink(&buf, SinkOptions{})
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	if err := s.Write(context.Background(), Event{Statement: "SELECT 1"}); !errors.Is(err, ErrSinkClosed) {
		t.Fatalf("Write after Close = %v, want ErrSinkClosed", err)
	}
	if buf.Len() != 0 {
		t.Errorf("a write after Close reached the writer: %q", buf.String())
	}
}

func TestJSONLSinkCloseFlushesBufferedWriter(t *testing.T) {
	var under bytes.Buffer
	bw := bufio.NewWriter(&under)
	s := NewJSONLSink(bw, SinkOptions{})

	if err := s.Write(context.Background(), Event{Kind: KindStatement, Statement: "SELECT 1"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if under.Len() != 0 {
		t.Fatal("test is not exercising buffering; the record already reached the file")
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !strings.Contains(under.String(), "SELECT 1") {
		t.Errorf("Close did not flush the buffered record: %q", under.String())
	}
}

// syncRecorder stands in for an *os.File: it counts Sync calls.
type syncRecorder struct {
	bytes.Buffer
	syncs int
	err   error
}

func (s *syncRecorder) Sync() error { s.syncs++; return s.err }

func TestJSONLSinkCloseSyncsAndReportsSyncFailure(t *testing.T) {
	boom := errors.New("disk gone")
	rec := &syncRecorder{err: boom}
	s := NewJSONLSink(rec, SinkOptions{})

	err := s.Close()
	if !errors.Is(err, boom) {
		t.Fatalf("Close = %v, want the Sync error wrapped", err)
	}
	if rec.syncs != 1 {
		t.Fatalf("Sync called %d times, want 1", rec.syncs)
	}
	if err := s.Close(); err != nil {
		t.Errorf("second Close = %v, want nil", err)
	}
	if rec.syncs != 1 {
		t.Errorf("second Close synced again (%d total); Close must be idempotent", rec.syncs)
	}
}

func TestRedactionReplacesStatementWithStableFingerprint(t *testing.T) {
	const stmt = "SELECT * FROM patients WHERE ssn = '123-45-6789'"

	var buf bytes.Buffer
	s := NewJSONLSink(&buf, SinkOptions{RedactStatements: true})
	for range 2 {
		if err := s.Write(context.Background(), Event{Statement: stmt}); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := s.Write(context.Background(), Event{Statement: stmt + " AND x = 1"}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	raw := buf.String()
	if strings.Contains(raw, "123-45-6789") || strings.Contains(raw, "patients") {
		t.Fatalf("redaction leaked the statement text: %q", raw)
	}

	got := decodeLines(t, raw)
	if got[0].Statement != got[1].Statement {
		t.Errorf("the same statement produced different fingerprints: %q vs %q", got[0].Statement, got[1].Statement)
	}
	if got[0].Statement == got[2].Statement {
		t.Error("different statements produced the same fingerprint")
	}
	if !strings.HasPrefix(got[0].Statement, "sha256:") {
		t.Errorf("fingerprint = %q, want a sha256: prefix", got[0].Statement)
	}
	if hexPart := strings.TrimPrefix(got[0].Statement, "sha256:"); len(hexPart) != 16 {
		t.Errorf("fingerprint hex length = %d, want 16", len(hexPart))
	}
}

func TestRedactionCoversHTTPBodyWithoutMutatingTheCaller(t *testing.T) {
	detail := &inspect.HTTPDetail{Method: "POST", Path: "/login", Body: `{"password":"hunter2"}`}

	var buf bytes.Buffer
	s := NewJSONLSink(&buf, SinkOptions{RedactStatements: true})
	if err := s.Write(context.Background(), Event{Statement: "POST /login", HTTP: detail}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if strings.Contains(buf.String(), "hunter2") {
		t.Fatalf("redaction left the HTTP body in the record: %q", buf.String())
	}
	if detail.Body != `{"password":"hunter2"}` {
		t.Errorf("the sink mutated the caller's HTTPDetail: Body = %q", detail.Body)
	}
	got := decodeLines(t, buf.String())[0]
	if got.HTTP == nil || !strings.HasPrefix(got.HTTP.Body, "sha256:") {
		t.Errorf("HTTP.Body = %+v, want a fingerprint", got.HTTP)
	}
	if got.HTTP.Path != "/login" {
		t.Errorf("redaction clobbered a non-body field: Path = %q", got.HTTP.Path)
	}
}

func TestRedactionLeavesEmptyStatementEmpty(t *testing.T) {
	var buf bytes.Buffer
	s := NewJSONLSink(&buf, SinkOptions{RedactStatements: true})
	if err := s.Write(context.Background(), Event{Kind: KindSessionStart}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := decodeLines(t, buf.String())[0]; got.Statement != "" {
		t.Errorf("Statement = %q, want empty: fingerprinting nothing yields a constant that looks like a real query", got.Statement)
	}
}

func TestTruncationBoundary(t *testing.T) {
	tests := []struct {
		name string
		max  int
		in   string
		want string
	}{
		{"exactly at the limit is untouched", 8, "12345678", "12345678"},
		{"one under the limit is untouched", 8, "1234567", "1234567"},
		{"one over the limit is cut", 8, "123456789", "12345678" + truncationMarker},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			s := NewJSONLSink(&buf, SinkOptions{MaxStatementBytes: tc.max})
			if err := s.Write(context.Background(), Event{Statement: tc.in}); err != nil {
				t.Fatalf("Write: %v", err)
			}
			if got := decodeLines(t, buf.String())[0].Statement; got != tc.want {
				t.Errorf("Statement = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTruncationCutsOnARuneBoundary(t *testing.T) {
	// "é" is two bytes, so a limit of 5 lands mid-sequence.
	var buf bytes.Buffer
	s := NewJSONLSink(&buf, SinkOptions{MaxStatementBytes: 5})
	if err := s.Write(context.Background(), Event{Statement: "abcdéf"}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got := decodeLines(t, buf.String())[0].Statement
	if got != "abcd"+truncationMarker {
		t.Errorf("Statement = %q, want the cut backed off to the rune boundary", got)
	}
	if strings.ContainsRune(got, '\uFFFD') {
		t.Error("truncation split a rune and the encoder emitted U+FFFD")
	}
}

func TestDefaultMaxStatementBytesApplies(t *testing.T) {
	var buf bytes.Buffer
	s := NewJSONLSink(&buf, SinkOptions{})
	if err := s.Write(context.Background(), Event{Statement: strings.Repeat("x", DefaultMaxStatementBytes+10)}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := decodeLines(t, buf.String())[0].Statement
	if want := DefaultMaxStatementBytes + len(truncationMarker); len(got) != want {
		t.Errorf("len(Statement) = %d, want %d", len(got), want)
	}
}

func TestRedactionWinsOverTruncation(t *testing.T) {
	// The precedence rule: the fingerprint must cover the ORIGINAL text, so
	// changing MaxStatementBytes does not change the fingerprint. Otherwise
	// records written before and after a config change stop correlating.
	long := "SELECT " + strings.Repeat("a", 500)

	fingerprintAt := func(max int) string {
		var buf bytes.Buffer
		s := NewJSONLSink(&buf, SinkOptions{RedactStatements: true, MaxStatementBytes: max})
		if err := s.Write(context.Background(), Event{Statement: long}); err != nil {
			t.Fatalf("Write: %v", err)
		}
		return decodeLines(t, buf.String())[0].Statement
	}

	tight, loose := fingerprintAt(16), fingerprintAt(4096)
	if tight != loose {
		t.Errorf("fingerprint depends on MaxStatementBytes: %q at 16 vs %q at 4096", tight, loose)
	}
	if strings.Contains(tight, truncationMarker) {
		t.Errorf("a redacted statement carries a truncation marker: %q", tight)
	}
	if want := fingerprint(long); tight != want {
		t.Errorf("fingerprint = %q, want the hash of the full statement %q", tight, want)
	}
}

// failingSink records writes and always fails, standing in for a full disk.
type failingSink struct {
	mu       sync.Mutex
	writes   int
	closes   int
	writeErr error
	closeErr error
}

func (f *failingSink) Write(context.Context, Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes++
	return f.writeErr
}

func (f *failingSink) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closes++
	return f.closeErr
}

func TestMultiSinkContinuesPastAFailingSink(t *testing.T) {
	first := errors.New("local disk full")
	second := errors.New("collector unreachable")

	bad := &failingSink{writeErr: first, closeErr: errors.New("close boom")}
	alsoBad := &failingSink{writeErr: second}
	good := NewMemorySink(4)

	m := NewMultiSink(bad, alsoBad, good)
	err := m.Write(context.Background(), Event{Kind: KindStatement, Statement: "SELECT 1"})
	if err == nil {
		t.Fatal("Write = nil, want the sink failures reported")
	}
	if !errors.Is(err, first) || !errors.Is(err, second) {
		t.Errorf("Write = %v, want both failures joined", err)
	}
	if good.Len() != 1 {
		t.Errorf("the healthy sink got %d events; a failing sink short-circuited the fan-out", good.Len())
	}
	if alsoBad.writes != 1 {
		t.Errorf("the second sink was written %d times, want 1", alsoBad.writes)
	}

	if err := m.Close(); err == nil {
		t.Error("Close = nil, want the close failure reported")
	}
	if bad.closes != 1 {
		t.Errorf("bad.closes = %d, want 1", bad.closes)
	}
	if err := m.Close(); err != nil {
		t.Errorf("second Close = %v, want nil", err)
	}
	if bad.closes != 1 {
		t.Errorf("second Close re-closed the children (%d total)", bad.closes)
	}
	if err := m.Write(context.Background(), Event{}); !errors.Is(err, ErrSinkClosed) {
		t.Errorf("Write after Close = %v, want ErrSinkClosed", err)
	}
}

func TestMultiSinkAllHealthyReturnsNil(t *testing.T) {
	a, b := NewMemorySink(2), NewMemorySink(2)
	m := NewMultiSink(a, b)
	if err := m.Write(context.Background(), Event{Statement: "SELECT 1"}); err != nil {
		t.Fatalf("Write = %v, want nil", err)
	}
	if a.Len() != 1 || b.Len() != 1 {
		t.Errorf("fan-out missed a sink: a=%d b=%d", a.Len(), b.Len())
	}
}

func TestMemorySinkRingEvictsOldestFirst(t *testing.T) {
	m := NewMemorySink(3)
	for _, s := range []string{"a", "b", "c", "d", "e"} {
		if err := m.Write(context.Background(), Event{Statement: s}); err != nil {
			t.Fatalf("Write %q = %v, want nil: a full ring drops, it does not fail", s, err)
		}
	}

	if m.Len() != 3 {
		t.Fatalf("Len = %d, want 3", m.Len())
	}
	got := m.Events()
	want := []string{"c", "d", "e"}
	for i := range want {
		if got[i].Statement != want[i] {
			t.Fatalf("Events() = %v, want %v (oldest first, oldest evicted)", stmts(got), want)
		}
	}
	if m.Dropped() != 2 {
		t.Errorf("Dropped = %d, want 2", m.Dropped())
	}
}

func stmts(evs []Event) []string {
	out := make([]string, len(evs))
	for i, ev := range evs {
		out[i] = ev.Statement
	}
	return out
}

func TestMemorySinkEventsSnapshotIsIndependent(t *testing.T) {
	m := NewMemorySink(2)
	m.Write(context.Background(), Event{Statement: "a"})

	snap := m.Events()
	m.Write(context.Background(), Event{Statement: "b"})
	m.Write(context.Background(), Event{Statement: "c"}) // evicts "a"

	if len(snap) != 1 || snap[0].Statement != "a" {
		t.Errorf("snapshot changed under later writes: %v", stmts(snap))
	}
}

func TestMemorySinkCloseKeepsEventsReadable(t *testing.T) {
	m := NewMemorySink(2)
	m.Write(context.Background(), Event{Statement: "a"})
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if m.Len() != 1 {
		t.Errorf("Close discarded the buffer; shutdown is when someone wants to read it")
	}
	if err := m.Write(context.Background(), Event{}); !errors.Is(err, ErrSinkClosed) {
		t.Errorf("Write after Close = %v, want ErrSinkClosed", err)
	}
}

func TestMemorySinkConcurrentWritesLoseNothing(t *testing.T) {
	const goroutines, perGoroutine = 16, 64
	const total = goroutines * perGoroutine

	m := NewMemorySink(total)
	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range perGoroutine {
				ev := Event{Principal: "p", Message: string(rune('a' + g%26)), MaskedCount: i}
				if err := m.Write(context.Background(), ev); err != nil {
					t.Errorf("Write: %v", err)
				}
			}
		}()
	}
	// Concurrent readers, so -race covers Events/Len against Write.
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				_ = m.Len()
				_ = m.Events()
			}
		}()
	}
	wg.Wait()

	if m.Len() != total {
		t.Errorf("Len = %d, want %d events retained", m.Len(), total)
	}
	if m.Dropped() != 0 {
		t.Errorf("Dropped = %d on an exactly-sized ring, want 0", m.Dropped())
	}
}

// blockingSink parks every write until released, so a test can fill a queue.
type blockingSink struct {
	release   chan struct{}
	releaseAt sync.Once
	mu        sync.Mutex
	got       []Event
}

func newBlockingSink() *blockingSink {
	return &blockingSink{release: make(chan struct{})}
}

// unblock releases parked writes. Idempotent so a test can both defer it as a
// safety net and call it inline at the point it wants the drain to proceed.
func (b *blockingSink) unblock() {
	b.releaseAt.Do(func() { close(b.release) })
}

func (b *blockingSink) Write(_ context.Context, ev Event) error {
	<-b.release
	b.mu.Lock()
	defer b.mu.Unlock()
	b.got = append(b.got, ev)
	return nil
}

func (b *blockingSink) Close() error { return nil }

func (b *blockingSink) events() []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]Event(nil), b.got...)
}

func TestAsyncSinkReturnsErrQueueFullWithoutBlocking(t *testing.T) {
	inner := newBlockingSink()
	defer inner.unblock()
	a := NewAsyncSink(inner, 2)

	// The drain goroutine takes one event and parks in inner.Write, so the
	// queue accepts queueSize more and then has no room. The fill runs off
	// the test goroutine on purpose: a Write that blocks must fail this test
	// on a deadline rather than hang the package until the panic timeout.
	type fill struct {
		accepted int
		err      error
	}
	res := make(chan fill, 1)
	go func() {
		accepted := 0
		for accepted <= 16 {
			if err := a.Write(context.Background(), Event{Statement: "SELECT 1"}); err != nil {
				res <- fill{accepted, err}
				return
			}
			accepted++
		}
		res <- fill{accepted, nil}
	}()

	var got fill
	select {
	case got = <-res:
	case <-time.After(2 * time.Second):
		t.Fatal("Write blocked on a full queue instead of returning ErrQueueFull")
	}

	if got.err == nil {
		t.Fatalf("queue accepted %d events with a parked drain and queueSize 2", got.accepted)
	}
	if !errors.Is(got.err, ErrQueueFull) {
		t.Fatalf("Write on a full queue = %v, want ErrQueueFull", got.err)
	}
	if got.accepted < 2 {
		t.Errorf("queue accepted only %d events, want at least queueSize=2", got.accepted)
	}

	inner.unblock()
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if n := len(inner.events()); n != got.accepted {
		t.Errorf("inner received %d events, want the %d that Write accepted: Close must drain", n, got.accepted)
	}
}

func TestAsyncSinkCloseDrainsAndIsIdempotent(t *testing.T) {
	mem := NewMemorySink(64)
	a := NewAsyncSink(mem, 32)

	for i := range 20 {
		if err := a.Write(context.Background(), Event{Statement: "SELECT 1", MaskedCount: i}); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if mem.Len() != 20 {
		t.Errorf("inner has %d events after Close, want 20: Close must drain the queue", mem.Len())
	}

	done := make(chan error, 1)
	go func() { done <- a.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("second Close = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second Close deadlocked")
	}

	if err := a.Write(context.Background(), Event{}); !errors.Is(err, ErrSinkClosed) {
		t.Errorf("Write after Close = %v, want ErrSinkClosed", err)
	}
}

func TestAsyncSinkCloseFromInsideAWritePathDoesNotDeadlock(t *testing.T) {
	// A caller that treats a failed audit write as fatal may shut the sink
	// down from the goroutine that just called Write. That must not hang.
	a := NewAsyncSink(NewMemorySink(4), 1)
	if err := a.Write(context.Background(), Event{Statement: "SELECT 1"}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		if err := a.Write(context.Background(), Event{Statement: "SELECT 2"}); err != nil && !errors.Is(err, ErrQueueFull) {
			done <- err
			return
		}
		done <- a.Close()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close from a Write path: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close from a Write path deadlocked")
	}
}

func TestAsyncSinkCloseReportsInnerWriteFailures(t *testing.T) {
	boom := errors.New("collector rejected the batch")
	inner := &failingSink{writeErr: boom}
	a := NewAsyncSink(inner, 8)

	for range 3 {
		if err := a.Write(context.Background(), Event{Statement: "SELECT 1"}); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	err := a.Close()
	if !errors.Is(err, boom) {
		t.Fatalf("Close = %v, want the drain goroutine's failure surfaced", err)
	}
	if inner.closes != 1 {
		t.Errorf("inner.closes = %d, want 1", inner.closes)
	}
}

func TestAsyncSinkConcurrentWritersAndCloseAreRaceFree(t *testing.T) {
	mem := NewMemorySink(4096)
	a := NewAsyncSink(mem, 256)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 64 {
				err := a.Write(context.Background(), Event{Statement: "SELECT 1", MaskedCount: i})
				if err != nil && !errors.Is(err, ErrQueueFull) && !errors.Is(err, ErrSinkClosed) {
					t.Errorf("Write: %v", err)
				}
			}
		}()
	}
	wg.Wait()

	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if mem.Len() == 0 {
		t.Error("nothing reached the inner sink")
	}
}

func TestAsyncSinkZeroQueueSizeDoesNotBlock(t *testing.T) {
	inner := newBlockingSink()
	defer inner.unblock()
	a := NewAsyncSink(inner, 0)

	// With the drain goroutine parked in inner.Write, an unbuffered handoff
	// has no receiver, so Write must fail rather than wait.
	_ = a.Write(context.Background(), Event{Statement: "first"})

	done := make(chan error, 1)
	go func() { done <- a.Write(context.Background(), Event{Statement: "second"}) }()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, ErrQueueFull) {
			t.Fatalf("Write = %v, want nil or ErrQueueFull", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Write blocked on an unbuffered queue")
	}

	inner.unblock()
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestAsyncOverJSONLProducesWellFormedLines(t *testing.T) {
	var buf bytes.Buffer
	jsonl := NewJSONLSink(&buf, SinkOptions{Now: fixedClock(time.Unix(1, 0).UTC())})
	a := NewAsyncSink(jsonl, 512)

	var wg sync.WaitGroup
	var accepted int64
	var mu sync.Mutex
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 32 {
				if err := a.Write(context.Background(), Event{Kind: KindStatement, Statement: strings.Repeat("q", 200)}); err == nil {
					mu.Lock()
					accepted++
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()

	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	got := decodeLines(t, buf.String())
	if int64(len(got)) != accepted {
		t.Fatalf("got %d lines, want the %d accepted writes", len(got), accepted)
	}
	for i, ev := range got {
		if len(ev.Statement) != 200 {
			t.Fatalf("line %d torn: statement is %d bytes", i, len(ev.Statement))
		}
	}
}
