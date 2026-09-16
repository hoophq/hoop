package analytics

import (
	"encoding/json"
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

func TestFullQueueDropsAndReportsTheDrop(t *testing.T) {
	// A server that never answers holds the sender on its first batch, so
	// the queue behind it fills.
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	defer srv.Close()
	defer close(block)

	c := New(Options{Endpoint: srv.URL, WriteKey: "wk", HTTPClient: &http.Client{Timeout: time.Hour}})
	for range queueSize + batchSize + 10 {
		c.Track(EventUsage, nil)
	}
	c.mu.Lock()
	dropped := c.dropped
	c.mu.Unlock()
	if dropped == 0 {
		t.Fatal("expected drops once the queue filled; Track must never block")
	}
}

func TestCountersReportDeltas(t *testing.T) {
	cs := NewCounters()
	pg := cs.Lane("postgres")
	pg.Statement(false)
	pg.Statement(true)
	pg.Masked(3)
	cs.Lane("mysql") // touched, never used: must not appear
	cs.HeartbeatFailed()

	u := cs.Snapshot()
	if u.Statements != 2 || u.Denied != 1 || u.Masked != 3 || u.HeartbeatFailures != 1 {
		t.Fatalf("first snapshot = %+v", u)
	}
	if _, ok := u.ByProtocol["mysql"]; ok {
		t.Fatal("an idle protocol must be omitted")
	}
	if got := u.ByProtocol["postgres"]; got != (LaneUsage{Statements: 2, Denied: 1, Masked: 3}) {
		t.Fatalf("postgres = %+v", got)
	}

	pg.Statement(false)
	u = cs.Snapshot()
	if u.Statements != 1 || u.Denied != 0 || u.HeartbeatFailures != 0 {
		t.Fatalf("second snapshot must be a delta, got %+v", u)
	}
}

func TestIDFromTokenIsStableAndNotTheToken(t *testing.T) {
	a, b := IDFromToken("hsc_secret"), IDFromToken("hsc_secret")
	if a != b || a == "hsc_secret" || len(a) != 64 {
		t.Fatalf("id = %q", a)
	}
	if RandomID() == RandomID() {
		t.Fatal("RandomID must differ per call")
	}
}
