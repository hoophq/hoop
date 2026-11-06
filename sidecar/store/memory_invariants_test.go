package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/session"
)

// checkInvariants walks both lists and fails on any state a reader could
// observe as wrong: a broken link, an event of a session the store dropped,
// a byte count that disagrees with the retained events, or a budget held
// past with more than one event.
func checkInvariants(t *testing.T, m *MemoryStore) {
	t.Helper()
	m.mu.RLock()
	defer m.mu.RUnlock()

	var sum, n, lastSeq int64
	perSession := map[session.ID]int64{}
	var prev *memEvent
	for e := m.oldest; e != nil; e = e.next {
		if e.prev != prev {
			t.Fatalf("broken prev link at seq %d", e.Seq)
		}
		if e.Seq <= lastSeq {
			t.Fatalf("seq %d after %d", e.Seq, lastSeq)
		}
		if _, ok := m.sessions[e.SessionID]; !ok {
			t.Fatalf("seq %d belongs to session %q the store no longer holds", e.Seq, e.SessionID)
		}
		lastSeq, prev = e.Seq, e
		sum += e.size
		n++
		perSession[e.SessionID]++
	}
	if prev != m.newest {
		t.Fatal("newest is stale")
	}
	if sum != m.bytes {
		t.Fatalf("bytes = %d, retained events sum to %d", m.bytes, sum)
	}
	if m.bytes > m.maxBytes && n > 1 {
		t.Fatalf("%d events hold %d bytes past a %d-byte budget", n, m.bytes, m.maxBytes)
	}
	for id, s := range m.sessions {
		var c int64
		var last *memEvent
		for e := s.first; e != nil; e = e.sessionNext {
			if e.SessionID != id {
				t.Fatalf("session %q lists an event of %q", id, e.SessionID)
			}
			c++
			last = e
		}
		if last != s.last {
			t.Fatalf("session %q: last is stale", id)
		}
		if c != perSession[id] {
			t.Fatalf("session %q lists %d events, the arrival list %d", id, c, perSession[id])
		}
	}
	if len(m.order) != len(m.sessions) {
		t.Fatalf("order holds %d sessions, the map %d", len(m.order), len(m.sessions))
	}
}

// Both bounds evict at once, under concurrent writers and readers.
func TestConcurrentEvictionKeepsTheListsConsistent(t *testing.T) {
	m := NewMemoryStore(MemoryOptions{MaxSessions: 40, MaxBytes: 256 << 10})

	var stop atomic.Bool
	var readers sync.WaitGroup
	for r := range 4 {
		readers.Go(func() {
			for !stop.Load() {
				_, _ = m.Events(context.Background(), EventFilter{Limit: MaxLimit})
				_, _ = m.Events(context.Background(), EventFilter{SessionID: session.ID("pool-" + strconv.Itoa(r)), Limit: 50})
				_, _ = m.Stats(context.Background(), SessionFilter{})
				_, _ = m.Sessions(context.Background(), SessionFilter{})
			}
		})
	}

	var writers sync.WaitGroup
	for w := range 8 {
		writers.Go(func() {
			for i := range 20000 {
				// Even writers keep four sessions open; odd ones churn new
				// sessions past MaxSessions.
				id := session.ID("pool-" + strconv.Itoa(i%4))
				if w%2 == 1 {
					id = session.ID(fmt.Sprintf("churn-%d-%d", w, i/50))
				}
				ev := audit.Event{Kind: audit.KindStatement, SessionID: id, Principal: "p",
					Protocol: inspect.Postgres, Operation: inspect.OpSelect,
					Statement: "SELECT " + strconv.Itoa(i) + strings.Repeat("y", i%700),
					Tables:    []string{"t"}, Metadata: map[string]string{"k": "v"}}
				if i%997 == 0 {
					ev.HTTP = &inspect.HTTPDetail{Method: "GET", Path: "/x",
						Query: map[string][]string{"a": {"b"}}, Headers: map[string]string{"h": "v"}}
				}
				if err := m.Write(context.Background(), ev); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	writers.Wait()
	stop.Store(true)
	readers.Wait()
	checkInvariants(t, m)
}

// A session the budget emptied is still a session: the count bound evicts
// it like any other, and a late event starts it over with no stale count.
func TestASessionTheBudgetEmptiedIsEvictedWhole(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore(MemoryOptions{MaxSessions: 2, MaxBytes: 4 << 10})
	for i := range 10 {
		_ = m.Write(ctx, poolStatement("a", i))
	}
	for i := range 10 {
		_ = m.Write(ctx, poolStatement("b", i))
	}
	checkInvariants(t, m)
	if a, _ := m.Session(ctx, "a"); a.EventsDropped != 10 {
		t.Fatalf("a dropped %d events, want 10", a.EventsDropped)
	}

	_ = m.Write(ctx, poolStatement("c", 0))
	checkInvariants(t, m)
	if _, err := m.Session(ctx, "a"); err == nil {
		t.Fatal("a was not evicted")
	}

	_ = m.Write(ctx, poolStatement("a", 99))
	checkInvariants(t, m)
	if a, _ := m.Session(ctx, "a"); a.EventsDropped != 0 || a.StatementCount != 1 {
		t.Errorf("a started over with dropped %d, statements %d; want 0 and 1", a.EventsDropped, a.StatementCount)
	}
}
