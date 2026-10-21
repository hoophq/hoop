package liveevents

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoophq/hoop/common/log"
)

var fast = timings{
	probeEvery: 20 * time.Millisecond,
	probeWait:  20 * time.Millisecond,
	minBackoff: 5 * time.Millisecond,
	maxBackoff: 20 * time.Millisecond,
}

// fakeBus stands in for one Postgres: a NOTIFY reaches every listening
// session, unless deliver is off, as behind a pooler in transaction mode.
type fakeBus struct {
	mu        sync.Mutex
	listeners map[chan string]struct{}
	notified  int
	deliver   bool
}

func newFakeBus() *fakeBus {
	return &fakeBus{listeners: map[chan string]struct{}{}, deliver: true}
}

func (b *fakeBus) setDeliver(v bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.deliver = v
}

func (b *fakeBus) listening() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.listeners)
}

func (b *fakeBus) notifyCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.notified
}

type fakeTransport struct {
	bus        *fakeBus
	failListen atomic.Int32
	block      chan struct{}
}

func (t *fakeTransport) notify(ctx context.Context, payload string) error {
	if t.block != nil {
		select {
		case <-t.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	t.bus.mu.Lock()
	defer t.bus.mu.Unlock()
	t.bus.notified++
	if !t.bus.deliver {
		return nil
	}
	for ch := range t.bus.listeners {
		select {
		case ch <- payload:
		default:
		}
	}
	return nil
}

func (t *fakeTransport) listen(ctx context.Context, ready func(), onPayload func(string)) error {
	if t.failListen.Add(-1) >= 0 {
		return errors.New("connection refused")
	}
	ch := make(chan string, 64)
	t.bus.mu.Lock()
	t.bus.listeners[ch] = struct{}{}
	t.bus.mu.Unlock()
	defer func() {
		t.bus.mu.Lock()
		delete(t.bus.listeners, ch)
		t.bus.mu.Unlock()
	}()
	ready()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case p := <-ch:
			onPayload(p)
		}
	}
}

func startHub(t *testing.T, tr transport) *Hub {
	t.Helper()
	h := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.run(ctx, tr, fast); close(done) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return h
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func receive(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("no event")
		return Event{}
	}
}

func nothing(t *testing.T, ch <-chan Event) {
	t.Helper()
	select {
	case ev := <-ch:
		t.Fatalf("unexpected event %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

var (
	logsMu sync.Mutex
	logs   []log.TailEntry
)

func init() {
	log.TailObserve(func(e log.TailEntry) {
		logsMu.Lock()
		logs = append(logs, e)
		logsMu.Unlock()
	})
}

// logsOf returns the messages a hub logged at level.
func logsOf(h *Hub, level string) []string {
	logsMu.Lock()
	defer logsMu.Unlock()
	var out []string
	for _, e := range logs {
		if e.Fields["replica"] == h.replica && strings.EqualFold(e.Level, level) {
			out = append(out, e.Message)
		}
	}
	return out
}

func TestAnEventReachesTheOtherReplicaOnce(t *testing.T) {
	bus := newFakeBus()
	a := startHub(t, &fakeTransport{bus: bus})
	b := startHub(t, &fakeTransport{bus: bus})
	eventually(t, "both replicas listen", func() bool { return bus.listening() == 2 })

	onA, stopA := a.Subscribe("org-1")
	defer stopA()
	onB, stopB := b.Subscribe("org-1")
	defer stopB()
	otherOrg, stopOther := a.Subscribe("org-2")
	defer stopOther()

	ev := Event{Type: SidecarCheckedIn, ID: "sc-1"}
	b.Publish("org-1", ev)

	if got := receive(t, onA); got != ev {
		t.Fatalf("replica A got %+v", got)
	}
	if got := receive(t, onB); got != ev {
		t.Fatalf("replica B got %+v", got)
	}
	nothing(t, onB)
	nothing(t, onA)
	nothing(t, otherOrg)
}

func TestPublishDoesNotWaitForTheDatabase(t *testing.T) {
	stuck := &fakeTransport{bus: newFakeBus(), block: make(chan struct{})}
	defer close(stuck.block)
	h := startHub(t, stuck)
	eventually(t, "the replica listens", func() bool { return stuck.bus.listening() == 1 })
	local, stop := h.Subscribe("org-1")
	defer stop()

	done := make(chan struct{})
	go func() {
		for i := 0; i < outboxSize*3; i++ {
			h.Publish("org-1", Event{Type: SidecarCheckedIn, ID: "sc-1"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish waited for the database")
	}
	receive(t, local)
	if got := logsOf(h, "warn"); !containsOnce(got, "NOTIFY queue is full") {
		t.Fatalf("want one full-queue warning, got %q", got)
	}
}

func TestTheNotifyPayloadRoundTrips(t *testing.T) {
	want := envelope{Replica: "r-1", OrgID: "org-1", Event: &Event{Type: SidecarCheckedIn, ID: "sc-1"}}
	payload, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got envelope
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestAnEventOverTheNotifyLimitStaysLocal(t *testing.T) {
	bus := newFakeBus()
	a := startHub(t, &fakeTransport{bus: bus})
	b := startHub(t, &fakeTransport{bus: bus})
	eventually(t, "both replicas listen", func() bool { return bus.listening() == 2 })
	onA, stopA := a.Subscribe("org-1")
	defer stopA()
	onB, stopB := b.Subscribe("org-1")
	defer stopB()

	big := Event{Type: SidecarCheckedIn, ID: strings.Repeat("x", maxPayload)}
	a.Publish("org-1", big)

	if got := receive(t, onA); got != big {
		t.Fatal("the publishing replica did not deliver the event")
	}
	nothing(t, onB)
	eventually(t, "the error log", func() bool {
		return containsOnce(logsOf(a, "error"), "over the NOTIFY limit")
	})
}

func TestTheProbeWarnsOnceAndLogsTheRecovery(t *testing.T) {
	bus := newFakeBus()
	bus.setDeliver(false)
	h := startHub(t, &fakeTransport{bus: bus})
	local, stop := h.Subscribe("org-1")
	defer stop()

	eventually(t, "the probe warning", func() bool {
		return len(matching(logsOf(h, "warn"), "did not come back")) > 0
	})
	time.Sleep(10 * fast.probeEvery)
	if got := matching(logsOf(h, "warn"), "did not come back"); len(got) != 1 {
		t.Fatalf("want one probe warning, got %d", len(got))
	}
	h.Publish("org-1", Event{Type: SidecarCheckedIn, ID: "sc-1"})
	receive(t, local)

	bus.setDeliver(true)
	eventually(t, "the recovery log", func() bool {
		return len(matching(logsOf(h, "info"), "works again")) > 0
	})
	time.Sleep(10 * fast.probeEvery)
	if got := matching(logsOf(h, "info"), "works again"); len(got) != 1 {
		t.Fatalf("want one recovery log, got %d", len(got))
	}
}

func TestListenReconnectsAfterAFailure(t *testing.T) {
	bus := newFakeBus()
	failing := &fakeTransport{bus: bus}
	failing.failListen.Store(3)
	a := startHub(t, failing)
	b := startHub(t, &fakeTransport{bus: bus})
	onA, stopA := a.Subscribe("org-1")
	defer stopA()

	a.Publish("org-1", Event{Type: SidecarCheckedIn, ID: "while-down"})
	if got := receive(t, onA); got.ID != "while-down" {
		t.Fatalf("got %+v", got)
	}

	eventually(t, "replica A listens again", func() bool { return bus.listening() == 2 })
	b.Publish("org-1", Event{Type: SidecarCheckedIn, ID: "after"})
	if got := receive(t, onA); got.ID != "after" {
		t.Fatalf("got %+v", got)
	}
	if got := matching(logsOf(a, "warn"), "LISTEN connection failed"); len(got) != 3 {
		t.Fatalf("want 3 reconnect warnings, got %d", len(got))
	}
	if got := matching(logsOf(a, "warn"), "did not come back"); len(got) != 0 {
		t.Fatalf("the probe warned while LISTEN reconnected: %q", got)
	}
}

func TestRunStopsWhenItsContextEnds(t *testing.T) {
	bus := newFakeBus()
	h := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.run(ctx, &fakeTransport{bus: bus}, fast); close(done) }()
	eventually(t, "the replica listens", func() bool { return bus.listening() == 1 })

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("run did not return")
	}
	if n := bus.listening(); n != 0 {
		t.Fatalf("%d sessions still listen", n)
	}
	sent := bus.notifyCount()
	h.Publish("org-1", Event{Type: SidecarCheckedIn})
	time.Sleep(50 * time.Millisecond)
	if bus.notifyCount() != sent {
		t.Fatal("a stopped hub still sends NOTIFY")
	}
}

func matching(msgs []string, substr string) []string {
	var out []string
	for _, m := range msgs {
		if strings.Contains(m, substr) {
			out = append(out, m)
		}
	}
	return out
}

func containsOnce(msgs []string, substr string) bool { return len(matching(msgs, substr)) == 1 }
