package analytics

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// capture is a Segment stand-in: it records every batch it is posted.
type capture struct {
	mu      sync.Mutex
	batches [][]message
	auth    string
	srv     *httptest.Server
}

func newCapture(t *testing.T) *capture {
	c := &capture{}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, _, _ := r.BasicAuth()
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			Batch []message `json:"batch"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("bad batch body: %v", err)
		}
		c.mu.Lock()
		c.auth = user
		c.batches = append(c.batches, payload.Batch)
		c.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func (c *capture) events() []message {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []message
	for _, b := range c.batches {
		out = append(out, b...)
	}
	return out
}

func TestTrackCarriesCommonPropertiesAndWriteKey(t *testing.T) {
	cap := newCapture(t)
	c := New(Options{
		Version:      "1.2.3",
		Entrypoint:   EntrypointBinary,
		SidecarID:    "abc",
		ControlPlane: true,
		Endpoint:     cap.srv.URL,
		WriteKey:     "wk_test",
	})
	c.Track(EventStarted, Properties{"lane-count": 2})
	c.Close()

	evs := cap.events()
	if len(evs) != 1 {
		t.Fatalf("got %d events, want 1", len(evs))
	}
	ev := evs[0]
	if ev.Event != string(EventStarted) || ev.AnonymousID != "abc" || ev.Type != "track" {
		t.Fatalf("event = %+v", ev)
	}
	for k, want := range map[string]any{
		"version": "1.2.3", "entrypoint": EntrypointBinary, "sidecar-id": "abc",
		"control-plane-connected": true, "lane-count": float64(2),
	} {
		if got := ev.Properties[k]; got != want {
			t.Errorf("property %s = %v (%T), want %v", k, got, got, want)
		}
	}
	if cap.auth != "wk_test" {
		t.Errorf("basic-auth user = %q, want the write key", cap.auth)
	}
	if ev.Context.Library.Name != Library {
		t.Errorf("library = %q", ev.Context.Library.Name)
	}
}

func TestDisabledWithoutKeyOrByEnv(t *testing.T) {
	if c := New(Options{}); c.Enabled() {
		t.Fatal("a client with no write key must be disabled")
	}
	t.Setenv(EnvVar, "off")
	if c := New(Options{WriteKey: "wk"}); c.Enabled() {
		t.Fatal("HOOP_SIDECAR_ANALYTICS=off must disable the client")
	}
	// The disabled client is inert, including the nil pointer.
	var nilClient *Client
	nilClient.Track(EventStarted, nil)
	nilClient.Close()
	(&Client{}).Track(EventStarted, nil)
	(&Client{}).Close()
}

// A full queue drops events and remembers how many. The count must survive
// every event that is itself dropped and arrive, whole, on the first one
// that gets through.
//
// The client is assembled without its sender, so the queue is the only
// capacity and the drop count is exact rather than a race against how far
// the sender got before the HTTP request parked it.
func TestFullQueueDropsAndReportsTheAccumulatedTotal(t *testing.T) {
	cap := newCapture(t)
	c := &Client{
		opts:   Options{Endpoint: cap.srv.URL, WriteKey: "wk", SidecarID: "abc"},
		common: Properties{},
		http:   cap.srv.Client(),
		queue:  make(chan message, queueSize),
		done:   make(chan struct{}),
	}
	const overflow = 30
	for range queueSize + overflow {
		c.Track(EventUsage, nil)
	}
	c.mu.Lock()
	dropped := c.dropped
	c.mu.Unlock()
	if dropped != overflow {
		t.Fatalf("dropped = %d, want %d: Track must never block and must count every drop", dropped, overflow)
	}

	// Start the sender, let it drain, then send the event that carries the
	// total.
	go c.run()
	deadline := time.Now().Add(5 * time.Second)
	for len(c.queue) > 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	c.Track(EventStopped, nil)
	c.Close()

	var reported []float64
	for _, ev := range cap.events() {
		if v, ok := ev.Properties["dropped-events"]; ok {
			reported = append(reported, v.(float64))
		}
	}
	if len(reported) != 1 || int(reported[0]) != overflow {
		t.Fatalf("dropped-events reported = %v, want exactly one event carrying %d", reported, overflow)
	}
	if got := len(cap.events()); got != queueSize+1 {
		t.Fatalf("delivered %d events, want %d queued + 1", got, queueSize+1)
	}
}

func TestCountersReportDeltas(t *testing.T) {
	cs := NewCounters()
	pg := cs.Lane("postgres")
	pg.Statement(false, "")
	pg.Statement(true, "operation")
	pg.Statement(true, "opa")
	pg.Statement(true, "") // an unnamed denial still lands in the breakdown
	pg.Masked(3)
	pg.AuditError()
	cs.Lane("mysql") // touched, never used: must not appear
	cs.HeartbeatFailed()

	u := cs.Snapshot()
	if u.Statements != 4 || u.Denied != 3 || u.Masked != 3 || u.AuditErrors != 1 || u.HeartbeatFailures != 1 {
		t.Fatalf("first snapshot = %+v", u)
	}
	if _, ok := u.ByProtocol["mysql"]; ok {
		t.Fatal("an idle protocol must be omitted")
	}
	if got := u.ByProtocol["postgres"]; got != (LaneUsage{Statements: 4, Denied: 3, Masked: 3}) {
		t.Fatalf("postgres = %+v", got)
	}
	if u.DeniedBy["operation"] != 1 || u.DeniedBy["opa"] != 1 || u.DeniedBy["unknown"] != 1 {
		t.Fatalf("denied-by = %v", u.DeniedBy)
	}
	var sum int64
	for _, n := range u.DeniedBy {
		sum += n
	}
	if sum != u.Denied {
		t.Fatalf("denied-by sums to %d, denied total is %d; the breakdown must partition the total", sum, u.Denied)
	}

	pg.Statement(false, "")
	u = cs.Snapshot()
	if u.Statements != 1 || u.Denied != 0 || u.HeartbeatFailures != 0 || len(u.DeniedBy) != 0 {
		t.Fatalf("second snapshot must be a delta, got %+v", u)
	}
}

// A denial is two writes, the total and its source. A snapshot racing the
// writer must see both or neither in one window, or denies-by-kind stops
// partitioning statements-denied. Hammered from several goroutines while
// snapshots are cut mid-flight; every window must balance.
func TestDeniedBreakdownPartitionsTheTotalUnderConcurrency(t *testing.T) {
	cs := NewCounters()
	l := cs.Lane("postgres")
	const writers, perWriter = 8, 2000
	var wg sync.WaitGroup
	for w := range writers {
		wg.Add(1)
		go func(src string) {
			defer wg.Done()
			for range perWriter {
				l.Statement(true, src)
			}
		}(fmt.Sprintf("kind-%d", w%3))
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	var totalDenied, totalByKind int64
	check := func(u Usage) {
		var sum int64
		for _, n := range u.DeniedBy {
			sum += n
		}
		if sum != u.Denied {
			t.Fatalf("window: denied=%d but breakdown sums to %d", u.Denied, sum)
		}
		totalDenied += u.Denied
		totalByKind += sum
	}
	for {
		select {
		case <-done:
			check(cs.Snapshot())
			if totalDenied != writers*perWriter || totalByKind != writers*perWriter {
				t.Fatalf("across windows: denied=%d by-kind=%d, want %d", totalDenied, totalByKind, writers*perWriter)
			}
			return
		default:
			check(cs.Snapshot())
		}
	}
}

// Segment bills per distinct anonymousId per month, so every id source must
// be stable across restarts of one install and must not leak its input.
func TestIDsAreStableAndNotTheirInputs(t *testing.T) {
	a, b := IDFromToken("hsc_secret"), IDFromToken("hsc_secret")
	if a != b || a == "hsc_secret" || len(a) != 64 {
		t.Fatalf("token id = %q", a)
	}
	h1, h2 := IDFromHost("tcp/:15432", "tcp/:8080"), IDFromHost("tcp/:15432", "tcp/:8080")
	if h1 != h2 || len(h1) != 64 {
		t.Fatalf("host id not stable: %q vs %q", h1, h2)
	}
	if IDFromHost("tcp/:15432") == h1 {
		t.Fatal("two installs on one host with different listeners must not share an id")
	}
	if IDFromHost() == IDFromToken("") {
		t.Fatal("a host id and a token id over empty input must not collide")
	}
	if RandomID() == RandomID() {
		t.Fatal("RandomID must differ per call")
	}
	// New with no id falls back to the host, not to a random per process.
	c1, c2 := New(Options{WriteKey: "wk", Endpoint: "http://127.0.0.1:1/"}), New(Options{WriteKey: "wk", Endpoint: "http://127.0.0.1:1/"})
	defer c1.Close()
	defer c2.Close()
	if c1.opts.SidecarID != c2.opts.SidecarID {
		t.Fatal("two clients on one host without an explicit id must share it")
	}
}

// HOOP_SIDECAR_ID outranks every derived id, is hashed rather than sent as
// written, and reaches a client that was given no id.
func TestOperatorIDOverridesAndIsHashed(t *testing.T) {
	if IDFromEnv() != "" {
		t.Fatal("IDFromEnv must be empty when the variable is unset")
	}
	t.Setenv(IDEnvVar, "billing-db-proxy")
	id := IDFromEnv()
	if id == "" || id == "billing-db-proxy" || len(id) != 64 {
		t.Fatalf("env id = %q, want a hash", id)
	}
	if id != IDFromEnv() {
		t.Fatal("env id not stable")
	}
	c := New(Options{WriteKey: "wk", Endpoint: "http://127.0.0.1:1/"})
	defer c.Close()
	if c.opts.SidecarID != id {
		t.Fatalf("New without an id = %q, want the operator's %q", c.opts.SidecarID, id)
	}
}
