package metrics

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoopinspect"
	"github.com/hoophq/hoopinspect/audit"
	"github.com/hoophq/hoopinspect/session"
	"github.com/hoophq/hoopinspect/store"
)

var base = time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)

// realisticSession is one session's worth of events in the order a gate emits
// them: start, statements interleaved with a violation and a masked response,
// an error, then end.
func realisticSession(id session.ID) []audit.Event {
	return []audit.Event{
		{
			Kind: audit.KindSessionStart, Timestamp: base,
			SessionID: id, Principal: "alice", Connection: "appdb",
			Protocol: hoopinspect.Postgres,
		},
		{
			Kind: audit.KindStatement, Timestamp: base.Add(1 * time.Second),
			SessionID: id, Principal: "alice", Connection: "appdb",
			Protocol: hoopinspect.Postgres, Operation: hoopinspect.OpSelect,
			Statement: "SELECT * FROM users", Tables: []string{"users"},
			Allowed: true, Direction: hoopinspect.FromClient,
			Duration: 10 * time.Millisecond,
		},
		{
			Kind: audit.KindStatement, Timestamp: base.Add(2 * time.Second),
			SessionID: id, Principal: "alice", Connection: "appdb",
			Protocol: hoopinspect.Postgres, Operation: hoopinspect.OpSelect,
			Statement: "SELECT * FROM users JOIN orders",
			Tables:    []string{"users", "orders"},
			Allowed:   true, Direction: hoopinspect.FromClient,
			Duration: 20 * time.Millisecond,
		},
		{
			Kind: audit.KindViolation, Timestamp: base.Add(3 * time.Second),
			SessionID: id, Principal: "alice", Connection: "appdb",
			Protocol: hoopinspect.Postgres, Operation: hoopinspect.OpDelete,
			Statement: "DELETE FROM users", Tables: []string{"users"},
			Allowed: false, Rule: "no-unbounded-delete",
			Message: "add a WHERE clause", Direction: hoopinspect.FromClient,
		},
		{
			Kind: audit.KindMasked, Timestamp: base.Add(4 * time.Second),
			SessionID: id, Principal: "alice", Connection: "appdb",
			Protocol: hoopinspect.Postgres, Direction: hoopinspect.FromServer,
			MaskedEntities: []string{"email", "ssn"}, MaskedCount: 7,
		},
		{
			Kind: audit.KindMasked, Timestamp: base.Add(5 * time.Second),
			SessionID: id, Principal: "alice", Connection: "appdb",
			Protocol: hoopinspect.Postgres, Direction: hoopinspect.FromServer,
			MaskedEntities: []string{"email"}, MaskedCount: 3,
		},
		{
			Kind: audit.KindError, Timestamp: base.Add(6 * time.Second),
			SessionID: id, Principal: "alice", Connection: "appdb",
			Protocol: hoopinspect.Postgres, Error: "upstream reset",
		},
		{
			Kind: audit.KindSessionEnd, Timestamp: base.Add(7 * time.Second),
			SessionID: id, Principal: "alice", Connection: "appdb",
			Protocol: hoopinspect.Postgres, Duration: 7500 * time.Millisecond,
			StatementCount: 3, DeniedCount: 1,
		},
	}
}

func accumulate(evs ...audit.Event) SessionMetrics {
	a := New()
	for _, ev := range evs {
		a.Add(ev)
	}
	return a.Snapshot()
}

func TestAccumulatorCounters(t *testing.T) {
	m := accumulate(realisticSession("s1")...)

	if m.Statements != 3 {
		t.Errorf("Statements = %d, want 3 (two allowed plus the violation)", m.Statements)
	}
	if m.Denied != 1 {
		t.Errorf("Denied = %d, want 1", m.Denied)
	}
	// Masked is values rewritten (7+3), not masked events (2).
	if m.Masked != 10 {
		t.Errorf("Masked = %d, want 10", m.Masked)
	}
	if m.Errors != 1 {
		t.Errorf("Errors = %d, want 1", m.Errors)
	}
	if m.SessionID != "s1" || m.Principal != "alice" || m.Connection != "appdb" {
		t.Errorf("identity = %q/%q/%q, want s1/alice/appdb", m.SessionID, m.Principal, m.Connection)
	}
	if m.Protocol != hoopinspect.Postgres {
		t.Errorf("Protocol = %q, want postgres", m.Protocol)
	}
	if !m.Completed {
		t.Error("Completed = false after session_end")
	}
	if got, want := m.Verdict(), store.VerdictDenied; got != want {
		t.Errorf("Verdict = %q, want %q (denial outranks the error)", got, want)
	}
}

func TestAccumulatorHistograms(t *testing.T) {
	m := accumulate(realisticSession("s1")...)

	wantOps := map[hoopinspect.Operation]int{
		hoopinspect.OpSelect: 2,
		hoopinspect.OpDelete: 1,
	}
	if !mapsEqual(m.ByOperation, wantOps) {
		t.Errorf("ByOperation = %v, want %v", m.ByOperation, wantOps)
	}

	// users appears in both selects and the delete; orders only in the join.
	wantTables := map[string]int{"users": 3, "orders": 1}
	if !mapsEqual(m.ByTable, wantTables) {
		t.Errorf("ByTable = %v, want %v", m.ByTable, wantTables)
	}

	wantRules := map[string]int{"no-unbounded-delete": 1}
	if !mapsEqual(m.ByRule, wantRules) {
		t.Errorf("ByRule = %v, want %v", m.ByRule, wantRules)
	}

	// email named by both masked events, ssn by one. Counting EVENTS, not
	// values: the events carry no per-class value split.
	wantEntities := map[string]int{"email": 2, "ssn": 1}
	if !mapsEqual(m.MaskedEntities, wantEntities) {
		t.Errorf("MaskedEntities = %v, want %v", m.MaskedEntities, wantEntities)
	}
}

func TestAccumulatorSkipsEmptyLabels(t *testing.T) {
	// A codec that could not classify emits an empty operation and an empty
	// table entry. Counting those produces a "" bar in every dashboard.
	m := accumulate(audit.Event{
		Kind: audit.KindStatement, Timestamp: base,
		Operation: "", Tables: []string{"", "users"}, Allowed: true,
	}, audit.Event{
		Kind: audit.KindMasked, Timestamp: base, MaskedEntities: []string{""},
	})

	if _, ok := m.ByOperation[""]; ok {
		t.Error("ByOperation counted the empty operation")
	}
	if _, ok := m.ByTable[""]; ok {
		t.Error("ByTable counted the empty table")
	}
	if m.ByTable["users"] != 1 {
		t.Errorf("ByTable[users] = %d, want 1", m.ByTable["users"])
	}
	if _, ok := m.MaskedEntities[""]; ok {
		t.Error("MaskedEntities counted the empty class")
	}
}

func TestAccumulatorDenialKeysOnKind(t *testing.T) {
	// Allowed's zero value is false. A statement event whose producer never
	// set it must not register as a denial — an audit counter that
	// over-reports violations gets ignored, and then the real one is too.
	m := accumulate(audit.Event{Kind: audit.KindStatement, Timestamp: base})
	if m.Denied != 0 {
		t.Errorf("Denied = %d for a KindStatement with zero-valued Allowed, want 0", m.Denied)
	}
	if m.Statements != 1 {
		t.Errorf("Statements = %d, want 1", m.Statements)
	}

	v := accumulate(audit.Event{Kind: audit.KindViolation, Timestamp: base, Allowed: false})
	if v.Denied != 1 || v.Statements != 1 {
		t.Errorf("violation: Statements=%d Denied=%d, want 1/1", v.Statements, v.Denied)
	}
}

func TestAccumulatorUnknownKindIgnored(t *testing.T) {
	// A newer producer emitting a kind this build does not know must not
	// disturb the counters it does know.
	m := accumulate(
		audit.Event{Kind: audit.KindStatement, Timestamp: base, Allowed: true},
		audit.Event{Kind: "quarantined", Timestamp: base.Add(time.Second), Allowed: true},
	)
	if m.Statements != 1 || m.Denied != 0 || m.Errors != 0 {
		t.Errorf("got %d/%d/%d statements/denied/errors, want 1/0/0", m.Statements, m.Denied, m.Errors)
	}
	// The unknown event still moves the clock: it happened in this session.
	if want := base.Add(time.Second); !m.LastEventAt.Equal(want) {
		t.Errorf("LastEventAt = %v, want %v", m.LastEventAt, want)
	}
}

func TestAccumulatorBytesByDirection(t *testing.T) {
	m := accumulate(
		audit.Event{
			Kind: audit.KindStatement, Timestamp: base,
			Statement: "SELECT 1", Direction: hoopinspect.FromClient, Allowed: true,
		},
		audit.Event{
			Kind: audit.KindStatement, Timestamp: base,
			Statement: "GET /x", Direction: hoopinspect.FromServer, Allowed: true,
			HTTP: &hoopinspect.HTTPDetail{Body: "hello"},
		},
		// No direction set: a statement is client-sent by definition.
		audit.Event{
			Kind: audit.KindStatement, Timestamp: base,
			Statement: "AB", Allowed: true,
		},
	)

	if want := int64(len("SELECT 1") + len("AB")); m.BytesIn != want {
		t.Errorf("BytesIn = %d, want %d", m.BytesIn, want)
	}
	if want := int64(len("GET /x") + len("hello")); m.BytesOut != want {
		t.Errorf("BytesOut = %d, want %d", m.BytesOut, want)
	}
}

func TestDurationPrefersSessionEnd(t *testing.T) {
	id := session.ID("s1")

	// While open, the event span is all we have.
	a := New()
	a.Add(audit.Event{Kind: audit.KindSessionStart, Timestamp: base, SessionID: id})
	a.Add(audit.Event{Kind: audit.KindStatement, Timestamp: base.Add(2 * time.Second), SessionID: id, Allowed: true})
	if got := a.Snapshot().Duration(); got != 2*time.Second {
		t.Errorf("open Duration = %v, want 2s (event span)", got)
	}

	// session_end reports accept-to-close, which is longer than the span
	// because the connection sat idle before closing. It must win.
	a.Add(audit.Event{
		Kind: audit.KindSessionEnd, Timestamp: base.Add(3 * time.Second),
		SessionID: id, Duration: 9 * time.Second,
	})
	if got := a.Snapshot().Duration(); got != 9*time.Second {
		t.Errorf("closed Duration = %v, want 9s (session_end wins over the 3s span)", got)
	}
}

func TestDurationEmpty(t *testing.T) {
	if got := (SessionMetrics{}).Duration(); got != 0 {
		t.Errorf("zero-value Duration = %v, want 0", got)
	}
}

func TestObserveTimeHandlesOutOfOrder(t *testing.T) {
	// Two direction pumps race, so events can be folded in out of timestamp
	// order. First/Last must be min/max, not first-seen/last-seen.
	m := accumulate(
		audit.Event{Kind: audit.KindStatement, Timestamp: base.Add(5 * time.Second), Allowed: true},
		audit.Event{Kind: audit.KindStatement, Timestamp: base.Add(1 * time.Second), Allowed: true},
		// A zero timestamp is missing data, not the epoch. Folding it in
		// would make FirstEventAt year 1 and Duration astronomical.
		audit.Event{Kind: audit.KindStatement, Allowed: true},
	)
	if want := base.Add(1 * time.Second); !m.FirstEventAt.Equal(want) {
		t.Errorf("FirstEventAt = %v, want %v", m.FirstEventAt, want)
	}
	if want := base.Add(5 * time.Second); !m.LastEventAt.Equal(want) {
		t.Errorf("LastEventAt = %v, want %v", m.LastEventAt, want)
	}
	if got := m.Duration(); got != 4*time.Second {
		t.Errorf("Duration = %v, want 4s", got)
	}
}

func TestIdentityFirstWins(t *testing.T) {
	// Identity is denormalized onto every event. A later event with a
	// different principal is corruption or a session-id collision; the first
	// value must stand rather than the audit trail silently reattributing.
	m := accumulate(
		audit.Event{Kind: audit.KindSessionStart, Timestamp: base, Principal: "alice", Connection: "appdb"},
		audit.Event{Kind: audit.KindStatement, Timestamp: base, Principal: "mallory", Connection: "other", Allowed: true},
	)
	if m.Principal != "alice" {
		t.Errorf("Principal = %q, want alice", m.Principal)
	}
	if m.Connection != "appdb" {
		t.Errorf("Connection = %q, want appdb", m.Connection)
	}
}

func TestIdentityFilledByLaterEventWhenStartMissing(t *testing.T) {
	// A process that starts mid-stream never sees session_start. The first
	// event carrying a principal must still populate it.
	m := accumulate(
		audit.Event{Kind: audit.KindStatement, Timestamp: base, Allowed: true},
		audit.Event{Kind: audit.KindStatement, Timestamp: base, Principal: "bob", Protocol: hoopinspect.MySQL, Allowed: true},
	)
	if m.Principal != "bob" || m.Protocol != hoopinspect.MySQL {
		t.Errorf("got %q/%q, want bob/mysql", m.Principal, m.Protocol)
	}
}

func TestSnapshotIsDeepCopy(t *testing.T) {
	a := New()
	for _, ev := range realisticSession("s1") {
		a.Add(ev)
	}

	snap := a.Snapshot()
	snap.ByOperation[hoopinspect.OpDrop] = 999
	snap.ByTable["users"] = 999
	snap.ByTable["injected"] = 999
	snap.ByRule["forged"] = 999
	snap.MaskedEntities["email"] = 999
	delete(snap.MaskedEntities, "ssn")

	after := a.Snapshot()
	if _, ok := after.ByOperation[hoopinspect.OpDrop]; ok {
		t.Error("mutating the snapshot's ByOperation reached the accumulator")
	}
	if after.ByTable["users"] != 3 {
		t.Errorf("ByTable[users] = %d after snapshot mutation, want 3", after.ByTable["users"])
	}
	if _, ok := after.ByTable["injected"]; ok {
		t.Error("mutating the snapshot's ByTable reached the accumulator")
	}
	if _, ok := after.ByRule["forged"]; ok {
		t.Error("mutating the snapshot's ByRule reached the accumulator")
	}
	if after.MaskedEntities["email"] != 2 || after.MaskedEntities["ssn"] != 1 {
		t.Errorf("MaskedEntities = %v after snapshot mutation, want email:2 ssn:1", after.MaskedEntities)
	}

	// Two snapshots must not alias each other either.
	s1, s2 := a.Snapshot(), a.Snapshot()
	s1.ByTable["users"] = 1
	if s2.ByTable["users"] != 3 {
		t.Error("two snapshots share the same ByTable map")
	}
}

func TestSnapshotConcurrentWithAdd(t *testing.T) {
	// The scenario this guards: a UI marshalling a snapshot while both
	// direction pumps write. Under -race a shared map shows up here; without
	// it, encoding/json throws "concurrent map iteration and map write".
	a := New()
	var wg sync.WaitGroup

	for w := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 500 {
				a.Add(audit.Event{
					Kind:      audit.KindStatement,
					Timestamp: base.Add(time.Duration(i) * time.Millisecond),
					Operation: hoopinspect.OpSelect,
					Tables:    []string{fmt.Sprintf("t%d", i%8)},
					Rule:      fmt.Sprintf("r%d", w),
					Statement: "SELECT 1",
					Allowed:   true,
					Duration:  time.Duration(i) * time.Microsecond,
				})
			}
		}()
	}

	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				if _, err := json.Marshal(a.Snapshot()); err != nil {
					t.Errorf("marshal snapshot: %v", err)
				}
			}
		}()
	}
	wg.Wait()

	if got := a.Snapshot().Statements; got != 2000 {
		t.Errorf("Statements = %d, want 2000", got)
	}
}

func TestLatencyEmpty(t *testing.T) {
	l := accumulate(audit.Event{Kind: audit.KindStatement, Timestamp: base, Allowed: true}).Latency
	if l.Count != 0 {
		t.Errorf("Count = %d for a statement with no duration, want 0", l.Count)
	}
	if l.P50 != 0 || l.P95 != 0 || l.P99 != 0 || l.Max != 0 {
		t.Errorf("percentiles = %v/%v/%v/%v on empty, want all zero", l.P50, l.P95, l.P99, l.Max)
	}
}

func TestLatencySingleSample(t *testing.T) {
	// One statement: every percentile is that statement. A UI showing p99=0
	// for a session that ran one 250ms query is lying about the only fact it
	// has.
	const d = 250 * time.Millisecond
	l := accumulate(audit.Event{
		Kind: audit.KindStatement, Timestamp: base, Allowed: true, Duration: d,
	}).Latency

	if l.Count != 1 {
		t.Fatalf("Count = %d, want 1", l.Count)
	}
	if l.Max != d {
		t.Errorf("Max = %v, want exactly %v", l.Max, d)
	}
	for _, p := range []struct {
		name string
		got  time.Duration
	}{{"p50", l.P50}, {"p95", l.P95}, {"p99", l.P99}} {
		if !within(p.got, d, 0.032) {
			t.Errorf("%s = %v, want %v within 3.2%%", p.name, p.got, d)
		}
	}
}

func TestLatencyPercentileAccuracy(t *testing.T) {
	// A known distribution: 1..1000 ms, one sample each. Nearest-rank exact
	// answers are p50=500ms, p95=950ms, p99=990ms, max=1000ms.
	a := New()
	for i := 1; i <= 1000; i++ {
		a.Add(audit.Event{
			Kind: audit.KindStatement, Timestamp: base, Allowed: true,
			Duration: time.Duration(i) * time.Millisecond,
		})
	}
	l := a.Snapshot().Latency

	if l.Count != 1000 {
		t.Fatalf("Count = %d, want 1000", l.Count)
	}
	if l.Max != 1000*time.Millisecond {
		t.Errorf("Max = %v, want exactly 1s — the slowest statement must not be rounded", l.Max)
	}
	for _, tc := range []struct {
		name string
		got  time.Duration
		want time.Duration
	}{
		{"p50", l.P50, 500 * time.Millisecond},
		{"p95", l.P95, 950 * time.Millisecond},
		{"p99", l.P99, 990 * time.Millisecond},
	} {
		if !within(tc.got, tc.want, 0.032) {
			t.Errorf("%s = %v, want %v within the documented 3.125%% bound", tc.name, tc.got, tc.want)
		}
	}
}

func TestLatencyAccuracyBoundHolds(t *testing.T) {
	// The documented guarantee is per-value: every recorded duration is
	// reported within 3.125%. Check it directly across nine octaves by
	// feeding one value at a time and reading it back as the max-rank
	// quantile.
	rng := rand.New(rand.NewPCG(7, 11))
	for range 2000 {
		d := time.Duration(rng.Int64N(int64(10 * time.Minute)))
		a := New()
		a.Add(audit.Event{Kind: audit.KindStatement, Timestamp: base, Allowed: true, Duration: d})
		got := a.Snapshot().Latency.P50
		if !within(got, d, 0.03125) {
			t.Fatalf("recorded %v, reported %v — outside the 3.125%% bound", d, got)
		}
	}
}

func TestLatencyMonotonicBuckets(t *testing.T) {
	// A non-monotone bucket mapping makes the cumulative scan in quantile
	// return a value from the wrong side of the distribution: the scan
	// assumes bucket order IS value order. Probe every octave boundary and
	// sub-bucket edge, then check both the index and its representative
	// value are non-decreasing.
	var probes []uint64
	for e := range 44 {
		for _, delta := range []int64{-1, 0, 1} {
			if v := int64(1)<<uint(e) + delta; v >= 0 {
				probes = append(probes, uint64(v))
			}
		}
		// Sub-bucket edges within the octave.
		for s := range latSubCount {
			step := uint64(1) << uint(e)
			probes = append(probes, step+uint64(s)*step/latSubCount)
		}
	}
	slices.Sort(probes)
	probes = slices.Compact(probes)

	prevBucket, prevValue := -1, uint64(0)
	for _, v := range probes {
		b := latBucket(v)
		if b < prevBucket {
			t.Fatalf("latBucket(%d) = %d after %d: index mapping is not monotone", v, b, prevBucket)
		}
		if b < 0 || b >= latBuckets {
			t.Fatalf("latBucket(%d) = %d, outside [0,%d)", v, b, latBuckets)
		}
		if rv := latValue(b); rv < prevValue {
			t.Fatalf("latValue(latBucket(%d)) = %d after %d: value mapping is not monotone", v, rv, prevValue)
		} else {
			prevValue = rv
		}
		prevBucket = b
	}
}

func TestLatencyNegativeDurationDropped(t *testing.T) {
	// A clock that stepped backwards yields a negative duration. Recording it
	// as zero would pull p50 down and hide the skew.
	l := accumulate(
		audit.Event{Kind: audit.KindStatement, Timestamp: base, Allowed: true, Duration: -5 * time.Second},
		audit.Event{Kind: audit.KindStatement, Timestamp: base, Allowed: true, Duration: 100 * time.Millisecond},
	).Latency

	if l.Count != 1 {
		t.Errorf("Count = %d, want 1 (the negative sample is dropped)", l.Count)
	}
	if !within(l.P50, 100*time.Millisecond, 0.032) {
		t.Errorf("P50 = %v, want ~100ms", l.P50)
	}
}

func TestLatencyIgnoresSessionEndDuration(t *testing.T) {
	// session_end carries the WHOLE session duration. Folding it into the
	// per-statement histogram makes p99 report the session length.
	l := accumulate(
		audit.Event{Kind: audit.KindStatement, Timestamp: base, Allowed: true, Duration: 5 * time.Millisecond},
		audit.Event{Kind: audit.KindSessionEnd, Timestamp: base, Duration: 2 * time.Hour},
	).Latency

	if l.Count != 1 {
		t.Errorf("Count = %d, want 1", l.Count)
	}
	if l.Max > time.Second {
		t.Errorf("Max = %v — the session duration leaked into the statement histogram", l.Max)
	}
}

func TestRegistryForIsStable(t *testing.T) {
	r := NewRegistry(4)
	a := r.For("s1")
	if r.For("s1") != a {
		t.Error("For returned a different accumulator for the same session")
	}
	if r.For("s2") == a {
		t.Error("For returned the same accumulator for a different session")
	}
	if r.Len() != 2 {
		t.Errorf("Len = %d, want 2", r.Len())
	}
}

func TestRegistrySnapshotAndForget(t *testing.T) {
	r := NewRegistry(4)
	r.For("s1").Add(audit.Event{Kind: audit.KindStatement, Timestamp: base, Allowed: true})

	m, ok := r.Snapshot("s1")
	if !ok || m.Statements != 1 {
		t.Errorf("Snapshot = %+v ok=%v, want 1 statement", m, ok)
	}
	if _, ok := r.Snapshot("nope"); ok {
		t.Error("Snapshot reported an unknown session as present")
	}

	r.Forget("s1")
	if _, ok := r.Snapshot("s1"); ok {
		t.Error("session survived Forget")
	}
	r.Forget("s1") // must not panic on a second call
}

func TestRegistryAll(t *testing.T) {
	r := NewRegistry(4)
	r.For("s1").Add(audit.Event{Kind: audit.KindStatement, Timestamp: base, Tables: []string{"a"}, Allowed: true})
	r.For("s2").Add(audit.Event{Kind: audit.KindViolation, Timestamp: base, Rule: "r"})

	all := r.All()
	if len(all) != 2 {
		t.Fatalf("All returned %d sessions, want 2", len(all))
	}
	if all["s1"].Statements != 1 || all["s1"].Denied != 0 {
		t.Errorf("s1 = %+v", all["s1"])
	}
	if all["s2"].Denied != 1 {
		t.Errorf("s2 Denied = %d, want 1", all["s2"].Denied)
	}

	// All hands out snapshots, so mutating one cannot reach the registry.
	all["s1"].ByTable["a"] = 42
	if r.All()["s1"].ByTable["a"] != 1 {
		t.Error("All returned live maps")
	}
}

func TestRegistryEvictsCompletedBeforeLive(t *testing.T) {
	r := NewRegistry(3)

	// Two completed sessions with distinct end times, plus one live.
	complete := func(id session.ID, at time.Time) {
		a := r.For(id)
		a.Add(audit.Event{Kind: audit.KindStatement, Timestamp: at, SessionID: id, Allowed: true})
		a.Add(audit.Event{Kind: audit.KindSessionEnd, Timestamp: at, SessionID: id, Duration: time.Second})
	}
	complete("old", base)
	complete("newer", base.Add(time.Hour))
	r.For("live").Add(audit.Event{Kind: audit.KindStatement, Timestamp: base, SessionID: "live", Allowed: true})

	// Over budget: the OLDEST COMPLETED goes, not the live one and not the
	// newer completed one.
	r.For("s4")
	if _, ok := r.Snapshot("old"); ok {
		t.Error("oldest completed session survived eviction")
	}
	if _, ok := r.Snapshot("newer"); !ok {
		t.Error("newer completed session was evicted before the older one")
	}
	if _, ok := r.Snapshot("live"); !ok {
		t.Error("a LIVE session was evicted — its numbers are the ones being watched")
	}
	if r.Len() != 3 {
		t.Errorf("Len = %d, want 3", r.Len())
	}
}

func TestRegistryKeepsLiveSessionsOverBudget(t *testing.T) {
	// Every session is live and the registry is full. Going over budget is
	// correct; dropping a running session is not.
	r := NewRegistry(2)
	for i := range 5 {
		id := session.ID(fmt.Sprintf("s%d", i))
		r.For(id).Add(audit.Event{Kind: audit.KindStatement, Timestamp: base, SessionID: id, Allowed: true})
	}
	if r.Len() != 5 {
		t.Fatalf("Len = %d, want 5 — a live session was evicted", r.Len())
	}

	// As soon as sessions complete, the overage is reclaimed on the next
	// insert.
	for i := range 4 {
		r.For(session.ID(fmt.Sprintf("s%d", i))).Add(audit.Event{
			Kind: audit.KindSessionEnd, Timestamp: base.Add(time.Duration(i) * time.Minute),
		})
	}
	r.For("s9")
	if r.Len() != 2 {
		t.Errorf("Len = %d after completion, want 2 (back within budget)", r.Len())
	}
	if _, ok := r.Snapshot("s4"); !ok {
		t.Error("the still-live session was evicted during reclaim")
	}
	if _, ok := r.Snapshot("s9"); !ok {
		t.Error("the newly created session was evicted")
	}
}

func TestRegistryEvictionKeepsWritesAlive(t *testing.T) {
	// A caller holding the pointer from For must keep working after the
	// registry drops the entry; returning a dead object would panic the data
	// path.
	r := NewRegistry(1)
	a := r.For("done")
	a.Add(audit.Event{Kind: audit.KindSessionEnd, Timestamp: base})
	r.For("next")

	if _, ok := r.Snapshot("done"); ok {
		t.Fatal("completed session was not evicted")
	}
	a.Add(audit.Event{Kind: audit.KindStatement, Timestamp: base, Allowed: true})
	if a.Snapshot().Statements != 1 {
		t.Error("writes to an evicted accumulator were lost")
	}
}

func TestRegistryDefaultBound(t *testing.T) {
	if got := NewRegistry(0).max; got != DefaultMaxSessions {
		t.Errorf("NewRegistry(0).max = %d, want %d", got, DefaultMaxSessions)
	}
	if got := NewRegistry(-5).max; got != DefaultMaxSessions {
		t.Errorf("NewRegistry(-5).max = %d, want %d", got, DefaultMaxSessions)
	}
}

func TestRegistryConcurrent(t *testing.T) {
	r := NewRegistry(8)
	var wg sync.WaitGroup

	for w := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 200 {
				id := session.ID(fmt.Sprintf("s%d", i%32))
				a := r.For(id)
				a.Add(audit.Event{
					Kind: audit.KindStatement, Timestamp: base, SessionID: id,
					Operation: hoopinspect.OpSelect, Tables: []string{"t"},
					Allowed: true, Duration: time.Millisecond,
				})
				if i%7 == 0 {
					a.Add(audit.Event{Kind: audit.KindSessionEnd, Timestamp: base, SessionID: id})
				}
				if w%2 == 0 {
					r.Snapshot(id)
				} else {
					r.All()
				}
				if i%13 == 0 {
					r.Forget(id)
				}
			}
		}()
	}
	wg.Wait()
}

func TestAggregateEmpty(t *testing.T) {
	s := Aggregate(nil)

	// The load-bearing assertion: NaN has no JSON encoding, so a NaN rate
	// fails the whole response and the dashboard renders nothing.
	if math.IsNaN(s.DeniedRate) {
		t.Fatal("DeniedRate is NaN on empty input")
	}
	if s.DeniedRate != 0 {
		t.Errorf("DeniedRate = %v, want 0", s.DeniedRate)
	}
	if _, err := json.Marshal(s); err != nil {
		t.Fatalf("marshal empty summary: %v", err)
	}
	if s.Sessions != 0 || s.Statements != 0 {
		t.Errorf("Sessions=%d Statements=%d, want 0/0", s.Sessions, s.Statements)
	}
	if s.P50DurationMS != 0 || s.P95DurationMS != 0 {
		t.Errorf("durations = %d/%d, want 0/0", s.P50DurationMS, s.P95DurationMS)
	}
}

func TestAggregateZeroStatementSessions(t *testing.T) {
	// Sessions that connected and ran nothing: statements is zero but the
	// session count is not. The rate must still be 0, not NaN.
	s := Aggregate([]store.SessionRecord{
		{ID: "a", Principal: "alice", StartedAt: base, EndedAt: base.Add(time.Second), DurationMS: 1000},
		{ID: "b", Principal: "bob", StartedAt: base, EndedAt: base.Add(time.Second), DurationMS: 2000},
	})
	if math.IsNaN(s.DeniedRate) || s.DeniedRate != 0 {
		t.Errorf("DeniedRate = %v, want 0", s.DeniedRate)
	}
	if s.Sessions != 2 {
		t.Errorf("Sessions = %d, want 2", s.Sessions)
	}
}

func TestAggregateTotalsAndRate(t *testing.T) {
	s := Aggregate([]store.SessionRecord{
		{
			ID: "a", Principal: "alice", Connection: "appdb", Protocol: hoopinspect.Postgres,
			StartedAt: base, EndedAt: base.Add(time.Second), DurationMS: 100,
			StatementCount: 8, DeniedCount: 1, MaskedCount: 4, ErrorCount: 0,
		},
		{
			ID: "b", Principal: "bob", Connection: "appdb", Protocol: hoopinspect.MySQL,
			StartedAt: base, EndedAt: base.Add(time.Second), DurationMS: 300,
			StatementCount: 2, DeniedCount: 1, MaskedCount: 0, ErrorCount: 2,
		},
	})

	if s.Sessions != 2 || s.Statements != 10 || s.Denied != 2 || s.Masked != 4 || s.Errors != 2 {
		t.Errorf("totals = %+v", s)
	}
	if s.DeniedRate != 0.2 {
		t.Errorf("DeniedRate = %v, want 0.2", s.DeniedRate)
	}
}

func TestAggregateDurationsExcludeOpenSessions(t *testing.T) {
	// An open session's DurationMS is zero. Counting those zeros drags the
	// median toward nothing exactly when traffic picks up.
	s := Aggregate([]store.SessionRecord{
		{ID: "a", StartedAt: base, EndedAt: base.Add(time.Second), DurationMS: 100},
		{ID: "b", StartedAt: base, EndedAt: base.Add(time.Second), DurationMS: 300},
		{ID: "open1", StartedAt: base},
		{ID: "open2", StartedAt: base},
		{ID: "open3", StartedAt: base},
	})
	if s.P50DurationMS != 100 {
		t.Errorf("P50DurationMS = %d, want 100 (nearest-rank over the two closed sessions)", s.P50DurationMS)
	}
	if s.P95DurationMS != 300 {
		t.Errorf("P95DurationMS = %d, want 300", s.P95DurationMS)
	}
}

func TestAggregateDurationPercentilesExact(t *testing.T) {
	recs := make([]store.SessionRecord, 0, 100)
	for i := 1; i <= 100; i++ {
		recs = append(recs, store.SessionRecord{
			ID: session.ID(fmt.Sprintf("s%d", i)), StartedAt: base,
			EndedAt: base.Add(time.Second), DurationMS: int64(i),
		})
	}
	s := Aggregate(recs)
	if s.P50DurationMS != 50 {
		t.Errorf("P50DurationMS = %d, want 50", s.P50DurationMS)
	}
	if s.P95DurationMS != 95 {
		t.Errorf("P95DurationMS = %d, want 95", s.P95DurationMS)
	}
}

func TestAggregateBreakdownsSortedAndTruncated(t *testing.T) {
	// 30 distinct principals with descending session counts. Only the top 20
	// may come back, in descending order.
	var recs []store.SessionRecord
	for p := range 30 {
		for range 30 - p {
			recs = append(recs, store.SessionRecord{
				Principal:  fmt.Sprintf("p%02d", p),
				Connection: "appdb",
				Protocol:   hoopinspect.Postgres,
				StartedAt:  base,
			})
		}
	}
	s := Aggregate(recs)

	if len(s.ByPrincipal) != store.TopN {
		t.Fatalf("ByPrincipal has %d entries, want %d", len(s.ByPrincipal), store.TopN)
	}
	if !slices.IsSortedFunc(s.ByPrincipal, func(a, b store.LabelCount) int { return int(b.Count - a.Count) }) {
		t.Errorf("ByPrincipal is not sorted descending: %v", s.ByPrincipal)
	}
	if s.ByPrincipal[0].Label != "p00" || s.ByPrincipal[0].Count != 30 {
		t.Errorf("top bar = %+v, want p00/30", s.ByPrincipal[0])
	}
	// The 21st-largest (p20, 10 sessions) must have been truncated away.
	for _, lc := range s.ByPrincipal {
		if lc.Label == "p20" {
			t.Error("a below-TopN principal survived truncation")
		}
	}
	// Breakdowns count sessions, so the single-label ones equal Sessions.
	if len(s.ByConnection) != 1 || s.ByConnection[0].Count != s.Sessions {
		t.Errorf("ByConnection = %v, want one bar of %d", s.ByConnection, s.Sessions)
	}
	if len(s.ByProtocol) != 1 || s.ByProtocol[0].Label != string(hoopinspect.Postgres) {
		t.Errorf("ByProtocol = %v", s.ByProtocol)
	}
}

func TestAggregateBreakdownTiesAreStable(t *testing.T) {
	// Equal counts must order by label, or the dashboard's bars shuffle on
	// every refresh and users think the data changed.
	recs := []store.SessionRecord{
		{Principal: "carol", StartedAt: base},
		{Principal: "alice", StartedAt: base},
		{Principal: "bob", StartedAt: base},
	}
	want := []string{"alice", "bob", "carol"}
	for range 20 {
		s := Aggregate(recs)
		got := make([]string, len(s.ByPrincipal))
		for i, lc := range s.ByPrincipal {
			got[i] = lc.Label
		}
		if !slices.Equal(got, want) {
			t.Fatalf("tie order = %v, want %v", got, want)
		}
	}
}

func TestAggregateSkipsEmptyLabels(t *testing.T) {
	// A raw TCP relay may know no principal. An empty label is missing data,
	// not a bar named "".
	s := Aggregate([]store.SessionRecord{{ID: "a", StartedAt: base}})
	if s.ByPrincipal != nil || s.ByConnection != nil || s.ByProtocol != nil {
		t.Errorf("empty labels produced bars: %v %v %v", s.ByPrincipal, s.ByConnection, s.ByProtocol)
	}
}

func within(got, want time.Duration, tol float64) bool {
	if want == 0 {
		return got == 0
	}
	return math.Abs(float64(got-want))/float64(want) <= tol
}

func mapsEqual[K comparable](a, b map[K]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
