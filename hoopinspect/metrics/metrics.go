// Package metrics turns the audit stream into the numbers a UI renders.
//
// # Separation from audit
//
// A sink writes and forgets, the right shape for the data path. A session
// detail page needs the running totals for a session that has not ended yet:
// "this query has been open for 40 seconds and has touched three tables" is
// the screen an operator watches during an incident, and a store that only
// learns the totals at close cannot answer it.
//
// An Accumulator sits beside the sink, fed the same events, and answers that
// question from memory. Nothing here persists; a restart loses the live
// numbers and the store keeps the durable ones.
//
// # Two scopes
//
//   - Accumulator / Registry: one live process, per session, updated per
//     event. This is the session detail page.
//   - Aggregate: a page of store.SessionRecord already read back from a
//     backend. This is the fleet dashboard.
//
// The two stay separate. Folding the fleet view into the live registry would
// mean the dashboard only sees sessions this process handled, which breaks
// the moment there are two replicas.
package metrics

import (
	"cmp"
	"maps"
	"math"
	"math/bits"
	"slices"
	"sync"
	"time"

	"github.com/hoophq/hoopinspect"
	"github.com/hoophq/hoopinspect/audit"
	"github.com/hoophq/hoopinspect/session"
	"github.com/hoophq/hoopinspect/store"
)

// Latency summarizes per-statement latency for one session.
//
// Values below Max come out of a fixed-bucket histogram and are therefore
// approximate; see the accuracy note on Accumulator. Max is tracked exactly,
// because the slowest statement is the one an operator goes looking for and
// rounding it loses the outlier that mattered.
type Latency struct {
	P50 time.Duration `json:"p50_ns"`
	P95 time.Duration `json:"p95_ns"`
	P99 time.Duration `json:"p99_ns"`
	Max time.Duration `json:"max_ns"`

	// Count is how many statements carried a latency. Statements without
	// one are absent here: a caller that never sets Event.Duration gets
	// zero while Statements climbs, and a UI must render "no data" rather
	// than "0ms".
	Count int64 `json:"count"`
}

// SessionMetrics is the accumulated view of one session.
//
// It is a value: every map in it is owned by the caller who received it, so
// serializing one cannot race the connection still writing events.
type SessionMetrics struct {
	SessionID  session.ID           `json:"session_id,omitempty"`
	Principal  string               `json:"principal,omitempty"`
	Connection string               `json:"connection,omitempty"`
	Protocol   hoopinspect.Protocol `json:"protocol,omitempty"`

	// Statements counts inspected statements, denied ones included. Denied
	// counts the subset that policy refused.
	Statements int `json:"statements"`
	Denied     int `json:"denied"`

	// Masked is the number of VALUES rewritten, summed over masked events
	// rather than counting the events. "We masked 412 values" is the number
	// a compliance reviewer asks for.
	Masked int `json:"masked"`

	// Errors counts transport or upstream failures.
	Errors int `json:"errors"`

	// ByOperation is the verb histogram.
	ByOperation map[hoopinspect.Operation]int `json:"by_operation,omitempty"`

	// ByTable counts how often each relation or resource was referenced.
	// Statements with no recognized tables contribute nothing: an empty
	// Tables means "the parser could not tell", never "touches nothing".
	ByTable map[string]int `json:"by_table,omitempty"`

	// ByRule counts policy rules that fired.
	ByRule map[string]int `json:"by_rule,omitempty"`

	// MaskedEntities counts how many masked EVENTS mentioned each PII class.
	// The unit is events: an event reports the classes it rewrote and a
	// single total, with no per-class breakdown, so summing MaskedCount
	// into every class named would multiply-count.
	MaskedEntities map[string]int `json:"masked_entities,omitempty"`

	// BytesIn and BytesOut measure INSPECTED PAYLOAD, not socket traffic:
	// statement text plus any captured HTTP body, attributed by direction.
	// An audit event carries no transport counter, and inventing one here
	// would report a number the proxy never measured. Bytes the codec framed
	// away, and bodies the codec was configured not to capture, are absent.
	BytesIn  int64 `json:"bytes_in"`
	BytesOut int64 `json:"bytes_out"`

	FirstEventAt time.Time `json:"first_event_at,omitzero"`
	LastEventAt  time.Time `json:"last_event_at,omitzero"`

	// SessionDuration is the length reported by the session_end event, which
	// measures accept to close. Zero while the session is open.
	SessionDuration time.Duration `json:"session_duration_ns,omitempty"`

	// Completed reports that a session_end event arrived.
	Completed bool `json:"completed"`

	Latency Latency `json:"latency"`
}

// Duration is how long the session ran.
//
// The session_end event wins when present because it measures accept to
// close, which includes the idle time before the first statement and after
// the last one. Falling back to the event span understates a session that sat
// open doing nothing, but it is the only thing knowable while it is still
// running.
func (m SessionMetrics) Duration() time.Duration {
	if m.SessionDuration > 0 {
		return m.SessionDuration
	}
	if m.FirstEventAt.IsZero() || m.LastEventAt.IsZero() {
		return 0
	}
	return m.LastEventAt.Sub(m.FirstEventAt)
}

// Verdict classifies the session the same way the store does, so a live row
// and a stored row cannot disagree about what colour the badge is.
func (m SessionMetrics) Verdict() string {
	return store.ClassifyVerdict(m.Denied, m.Errors)
}

// Latency histogram geometry.
//
// Buckets are log-linear: each power-of-two octave of nanoseconds is split
// into latSubCount equal sub-buckets. A value is reported as its bucket's
// midpoint, so the relative error is at most half a sub-bucket width over the
// octave floor: (2^(e-4)/2) / 2^e = 1/32, i.e. ACCURACY IS +/- 3.125% for any
// value >= 16ns, and exact below that.
//
// The trade-off: this is worse than keeping every sample and sorting, and
// better than a random reservoir, whose sampling error grows in the tail
// where p99 lives. It costs a flat 624 counters (about 2.4 KiB) per session
// no matter how many statements run. A session that stays open for a day and
// runs a million statements must not grow a million-element slice.
const (
	latSubBits  = 4
	latSubCount = 1 << latSubBits

	// latMaxExp caps the tracked range at 2^42 ns, about 73 minutes. A single
	// statement slower than that is pinned to the top bucket; its exact value
	// still shows up in Max.
	latMaxExp = 41

	latBuckets = (latMaxExp-latSubBits+1)*latSubCount + latSubCount
)

// latBucket maps a nanosecond count to its bucket index. The mapping is
// monotone, so a running sum over buckets is a valid quantile scan.
func latBucket(v uint64) int {
	if v < latSubCount {
		return int(v)
	}
	e := bits.Len64(v) - 1
	if e > latMaxExp {
		return latBuckets - 1
	}
	shift := uint(e - latSubBits)
	return (e-latSubBits+1)*latSubCount + int(v>>shift) - latSubCount
}

// latValue returns the representative value of a bucket: its midpoint, which
// halves the worst-case error compared to reporting either edge.
func latValue(i int) uint64 {
	if i < latSubCount {
		return uint64(i)
	}
	e := i/latSubCount + latSubBits - 1
	sub := uint64(i % latSubCount)
	shift := uint(e - latSubBits)
	lo := (latSubCount + sub) << shift
	return lo + (uint64(1)<<shift)/2
}

type histogram struct {
	buckets [latBuckets]uint32
	count   int64
	max     uint64
}

func (h *histogram) record(d time.Duration) {
	// A negative duration means the caller's clock went backwards. Recording
	// it as zero would pull p50 down and hide the skew; dropping it keeps the
	// distribution honest and Count tells you it was dropped.
	if d < 0 {
		return
	}
	v := uint64(d)
	if v > h.max {
		h.max = v
	}
	i := latBucket(v)
	if h.buckets[i] != math.MaxUint32 {
		h.buckets[i]++
	}
	h.count++
}

// quantile uses the nearest-rank definition: the smallest bucket whose
// cumulative count reaches ceil(p*n). With n==1 every quantile is that one
// sample, which is the answer a UI should show for a session that ran one
// query.
func (h *histogram) quantile(p float64) time.Duration {
	if h.count == 0 {
		return 0
	}
	rank := int64(math.Ceil(p * float64(h.count)))
	if rank < 1 {
		rank = 1
	}
	if rank > h.count {
		rank = h.count
	}
	var cum int64
	for i := range latBuckets {
		cum += int64(h.buckets[i])
		if cum >= rank {
			return time.Duration(latValue(i))
		}
	}
	// Unreachable while count is the sum of the buckets; saturated buckets
	// are the one way it is not, and the exact max is the best answer left.
	return time.Duration(h.max)
}

func (h *histogram) summary() Latency {
	return Latency{
		P50:   h.quantile(0.50),
		P95:   h.quantile(0.95),
		P99:   h.quantile(0.99),
		Max:   time.Duration(h.max),
		Count: h.count,
	}
}

// Accumulator folds audit events into one session's metrics.
//
// Safe for concurrent use: the request and response pumps are separate
// goroutines writing the same session, so an accumulator that needed external
// synchronization would move the mutex to every caller.
type Accumulator struct {
	mu sync.Mutex

	m    SessionMetrics
	lat  histogram
	done time.Time
}

// New returns an empty Accumulator.
func New() *Accumulator {
	return &Accumulator{
		m: SessionMetrics{
			ByOperation:    make(map[hoopinspect.Operation]int),
			ByTable:        make(map[string]int),
			ByRule:         make(map[string]int),
			MaskedEntities: make(map[string]int),
		},
	}
}

// Add folds one event in. Unknown kinds are ignored rather than rejected: a
// newer producer emitting a kind this build does not know must not corrupt
// the counters it does know.
func (a *Accumulator) Add(ev audit.Event) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.identify(ev)
	a.observeTime(ev.Timestamp)

	switch ev.Kind {
	case audit.KindStatement, audit.KindViolation:
		a.m.Statements++

		// Denial keys on the KIND, not on !Allowed. Allowed is a bool whose
		// zero value is false, so a hand-built Event{Kind: KindStatement}
		// that forgot to set it would otherwise register as a violation,
		// the direction an audit counter must never be wrong in.
		if ev.Kind == audit.KindViolation {
			a.m.Denied++
		}

		if ev.Operation != "" {
			a.m.ByOperation[ev.Operation]++
		}
		for _, t := range ev.Tables {
			if t != "" {
				a.m.ByTable[t]++
			}
		}
		if ev.Rule != "" {
			a.m.ByRule[ev.Rule]++
		}
		a.countBytes(ev)

		// Only statement events carry a per-statement latency. Duration on a
		// session_end event is the whole session and would swamp the tail.
		if ev.Duration > 0 {
			a.lat.record(ev.Duration)
		}

	case audit.KindMasked:
		a.m.Masked += ev.MaskedCount
		for _, e := range ev.MaskedEntities {
			if e != "" {
				a.m.MaskedEntities[e]++
			}
		}
		a.countBytes(ev)

	case audit.KindError:
		a.m.Errors++

	case audit.KindSessionEnd:
		a.m.Completed = true
		a.m.SessionDuration = ev.Duration
		a.done = ev.Timestamp
		if a.done.IsZero() {
			// A producer that left the timestamp unset still ended the
			// session; the registry needs an ordering key for eviction, and
			// "now" is a truthful one for an event we are handling now.
			a.done = time.Now().UTC()
		}
	}
}

// identify fills the session facts from whichever event supplies them first.
// They are denormalized onto every event, so the first non-empty value wins
// and later events cannot rewrite history.
func (a *Accumulator) identify(ev audit.Event) {
	if a.m.SessionID == "" {
		a.m.SessionID = ev.SessionID
	}
	if a.m.Principal == "" {
		a.m.Principal = ev.Principal
	}
	if a.m.Connection == "" {
		a.m.Connection = ev.Connection
	}
	if a.m.Protocol == "" {
		a.m.Protocol = ev.Protocol
	}
}

func (a *Accumulator) observeTime(ts time.Time) {
	if ts.IsZero() {
		return
	}
	if a.m.FirstEventAt.IsZero() || ts.Before(a.m.FirstEventAt) {
		a.m.FirstEventAt = ts
	}
	if ts.After(a.m.LastEventAt) {
		a.m.LastEventAt = ts
	}
}

func (a *Accumulator) countBytes(ev audit.Event) {
	n := int64(len(ev.Statement))
	if ev.HTTP != nil {
		n += int64(len(ev.HTTP.Body))
	}
	if n == 0 {
		return
	}
	// An empty Direction means client: a statement is something the client
	// sent, and every codec that omits the field is decoding a request.
	if ev.Direction == hoopinspect.FromServer {
		a.m.BytesOut += n
	} else {
		a.m.BytesIn += n
	}
}

// Snapshot returns an independent copy.
//
// The copy is deep, which is the reason this method exists. Handing back the
// live maps means a UI marshalling the result races the connection still
// writing events, and encoding/json reading a map under concurrent write is
// a fatal runtime throw, not a recoverable error.
func (a *Accumulator) Snapshot() SessionMetrics {
	a.mu.Lock()
	defer a.mu.Unlock()

	out := a.m
	out.ByOperation = maps.Clone(a.m.ByOperation)
	out.ByTable = maps.Clone(a.m.ByTable)
	out.ByRule = maps.Clone(a.m.ByRule)
	out.MaskedEntities = maps.Clone(a.m.MaskedEntities)
	out.Latency = a.lat.summary()
	return out
}

// Completed reports whether a session_end event arrived, and when.
func (a *Accumulator) Completed() (bool, time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.m.Completed, a.done
}

// DefaultMaxSessions bounds a Registry created with a non-positive size. It
// is generous enough that a mid-sized sidecar never evicts and small enough
// that the histograms stay under a few megabytes.
const DefaultMaxSessions = 1024

// Registry holds one Accumulator per live session.
//
// # The soft bound
//
// A registry that grows forever is a memory leak in a process that runs for
// months. A registry that evicts a session still running loses the numbers
// for the query someone is watching right now, the single case the live view
// exists for.
//
// Eviction therefore takes the OLDEST COMPLETED session, and when every
// entry is still live the registry goes over budget rather than dropping
// one. The ceiling stays bounded: it becomes the number of simultaneously
// open connections, which the transport already limits, and the overage is
// reclaimed as soon as anything completes.
type Registry struct {
	mu      sync.Mutex
	max     int
	entries map[session.ID]*Accumulator
}

// NewRegistry returns a Registry bounded at maxSessions. A value <= 0 uses
// DefaultMaxSessions.
func NewRegistry(maxSessions int) *Registry {
	if maxSessions <= 0 {
		maxSessions = DefaultMaxSessions
	}
	return &Registry{max: maxSessions, entries: make(map[session.ID]*Accumulator)}
}

// For returns the accumulator for id, creating it if needed. The returned
// pointer stays valid after eviction: a caller holding it keeps writing to a
// live object the registry no longer publishes, which beats handing back nil
// on the data path.
func (r *Registry) For(id session.ID) *Accumulator {
	r.mu.Lock()
	defer r.mu.Unlock()

	if a, ok := r.entries[id]; ok {
		return a
	}
	a := New()
	r.entries[id] = a
	r.evictLocked()
	return a
}

// evictLocked drops completed sessions, oldest first, until the registry is
// back within budget or nothing completed is left.
func (r *Registry) evictLocked() {
	for len(r.entries) > r.max {
		var (
			victim session.ID
			oldest time.Time
			found  bool
		)
		for id, a := range r.entries {
			done, at := a.Completed()
			if !done {
				continue
			}
			if !found || at.Before(oldest) {
				victim, oldest, found = id, at, true
			}
		}
		if !found {
			// Everything is live. Over budget beats losing a running
			// session's numbers.
			return
		}
		delete(r.entries, victim)
	}
}

// Snapshot returns the metrics for one session.
func (r *Registry) Snapshot(id session.ID) (SessionMetrics, bool) {
	r.mu.Lock()
	a, ok := r.entries[id]
	r.mu.Unlock()
	if !ok {
		return SessionMetrics{}, false
	}
	return a.Snapshot(), true
}

// Forget drops a session. Call it once its metrics have been persisted, so a
// completed session does not wait for eviction pressure to free its
// histogram.
func (r *Registry) Forget(id session.ID) {
	r.mu.Lock()
	delete(r.entries, id)
	r.mu.Unlock()
}

// All snapshots every tracked session.
//
// The registry lock is released before snapshotting so a slow marshaller
// cannot block the data path calling For.
func (r *Registry) All() map[session.ID]SessionMetrics {
	r.mu.Lock()
	live := make(map[session.ID]*Accumulator, len(r.entries))
	maps.Copy(live, r.entries)
	r.mu.Unlock()

	out := make(map[session.ID]SessionMetrics, len(live))
	for id, a := range live {
		out[id] = a.Snapshot()
	}
	return out
}

// Len reports how many sessions are tracked, including any overage above the
// configured bound.
func (r *Registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}

// Summary is the fleet view over a set of session records.
type Summary struct {
	Sessions   int64 `json:"sessions"`
	Statements int64 `json:"statements"`
	Denied     int64 `json:"denied"`
	Masked     int64 `json:"masked"`
	Errors     int64 `json:"errors"`

	// DeniedRate is Denied/Statements, and 0 when there are no statements.
	// Not NaN: NaN has no JSON representation, so encoding/json fails the
	// whole response and the dashboard renders nothing instead of a zero.
	DeniedRate float64 `json:"denied_rate"`

	// Breakdowns count SESSIONS per label, matching the Sessions total.
	// Sorted by count descending, ties broken by label ascending so a
	// dashboard's bar order does not shuffle between refreshes, then
	// truncated to store.TopN.
	ByPrincipal  []store.LabelCount `json:"by_principal,omitempty"`
	ByConnection []store.LabelCount `json:"by_connection,omitempty"`
	ByProtocol   []store.LabelCount `json:"by_protocol,omitempty"`

	// Duration percentiles over CLOSED sessions only. An open session's
	// DurationMS is zero, and folding those zeros in would drag the median
	// toward nothing every time traffic picked up.
	P50DurationMS int64 `json:"p50_duration_ms"`
	P95DurationMS int64 `json:"p95_duration_ms"`
}

// Aggregate summarizes a set of session records.
//
// It works on records already read from a store rather than issuing queries,
// so the same function serves a SQLite backend, a Postgres one and a test
// fixture. The caller decides the window by choosing what to pass in.
func Aggregate(records []store.SessionRecord) Summary {
	s := Summary{Sessions: int64(len(records))}

	byPrincipal := make(map[string]int64)
	byConnection := make(map[string]int64)
	byProtocol := make(map[string]int64)
	durations := make([]int64, 0, len(records))

	for _, r := range records {
		s.Statements += int64(r.StatementCount)
		s.Denied += int64(r.DeniedCount)
		s.Masked += int64(r.MaskedCount)
		s.Errors += int64(r.ErrorCount)

		if r.Principal != "" {
			byPrincipal[r.Principal]++
		}
		if r.Connection != "" {
			byConnection[r.Connection]++
		}
		if r.Protocol != "" {
			byProtocol[string(r.Protocol)]++
		}
		if !r.IsOpen() {
			durations = append(durations, r.DurationMS)
		}
	}

	if s.Statements > 0 {
		s.DeniedRate = float64(s.Denied) / float64(s.Statements)
	}

	s.ByPrincipal = topLabels(byPrincipal)
	s.ByConnection = topLabels(byConnection)
	s.ByProtocol = topLabels(byProtocol)

	slices.Sort(durations)
	s.P50DurationMS = nearestRank(durations, 0.50)
	s.P95DurationMS = nearestRank(durations, 0.95)

	return s
}

// topLabels sorts a breakdown descending and truncates it to store.TopN.
func topLabels(counts map[string]int64) []store.LabelCount {
	if len(counts) == 0 {
		return nil
	}
	out := make([]store.LabelCount, 0, len(counts))
	for label, n := range counts {
		out = append(out, store.LabelCount{Label: label, Count: n})
	}
	slices.SortFunc(out, func(a, b store.LabelCount) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(a.Label, b.Label))
	})
	if len(out) > store.TopN {
		out = out[:store.TopN]
	}
	return out
}

// nearestRank picks the ceil(p*n)-th smallest value of a sorted slice, the
// same definition the latency histogram uses so the two never disagree about
// what "p95" means.
func nearestRank(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(p * float64(len(sorted))))
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}
