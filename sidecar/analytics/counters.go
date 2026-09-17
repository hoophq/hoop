package analytics

import (
	"sync"
	"sync/atomic"
)

// Counters accumulates the data-path facts a usage event reports. One per
// process; every connection's gate writes into the LaneCounter for its
// protocol, and Snapshot reads the deltas since the previous snapshot.
//
// Lock-free on the allow path: a gate increments an atomic per statement,
// and nothing here is allowed to cost the data path more than that. The
// deny path takes a mutex, because a denial is two facts — the total and
// its source — and a snapshot must see both or neither, or the per-source
// breakdown stops partitioning the total within a window. Denials are the
// rare case.
type Counters struct {
	mu    sync.Mutex
	lanes map[string]*LaneCounter

	heartbeatFailures atomic.Int64
	prevHeartbeat     int64
}

// NewCounters returns an empty set.
func NewCounters() *Counters {
	return &Counters{lanes: make(map[string]*LaneCounter)}
}

// Lane returns the counter for a protocol, creating it on first use. Two
// lanes speaking the same protocol share one: usage is reported per
// protocol, never per lane, because lane names are operator data.
func (c *Counters) Lane(protocol string) *LaneCounter {
	c.mu.Lock()
	defer c.mu.Unlock()
	lc, ok := c.lanes[protocol]
	if !ok {
		lc = &LaneCounter{deniedBy: make(map[string]int64), prevDeniedBy: make(map[string]int64)}
		c.lanes[protocol] = lc
	}
	return lc
}

// HeartbeatFailed records one failed control plane handshake.
func (c *Counters) HeartbeatFailed() { c.heartbeatFailures.Add(1) }

// LaneCounter is the per-protocol write side. It satisfies gate.Metrics
// structurally; this package does not import gate so that gate never has a
// reason to import this one.
type LaneCounter struct {
	statements  atomic.Int64
	masked      atomic.Int64
	auditErrors atomic.Int64

	// denied and deniedBy are written together under mu, and Snapshot reads
	// them under it too, so the breakdown always sums to the total for the
	// same instant. prevDenied and prevDeniedBy are the baselines the last
	// Snapshot advanced to; they live under the same lock.
	mu           sync.Mutex
	denied       int64
	deniedBy     map[string]int64
	prevDenied   int64
	prevDeniedBy map[string]int64

	// prev* for the atomics, read and written under Counters.mu only.
	prevStatements, prevMasked, prevAuditErrors int64
}

// Statement records one judged statement. source is the denial's kind,
// empty when allowed; an unnamed denial is counted under "unknown" so the
// per-source breakdown always sums to the denied total.
func (l *LaneCounter) Statement(denied bool, source string) {
	l.statements.Add(1)
	if !denied {
		return
	}
	if source == "" {
		source = "unknown"
	}
	l.mu.Lock()
	l.denied++
	l.deniedBy[source]++
	l.mu.Unlock()
}

// Masked records values rewritten out of one response.
func (l *LaneCounter) Masked(values int) {
	if values > 0 {
		l.masked.Add(int64(values))
	}
}

// AuditError records one audit event the sink could not write.
func (l *LaneCounter) AuditError() { l.auditErrors.Add(1) }

// LaneUsage is one protocol's delta.
type LaneUsage struct {
	Statements int64 `json:"statements"`
	Denied     int64 `json:"denied"`
	Masked     int64 `json:"masked"`
}

// Usage is what Snapshot returns: process totals plus the per-protocol
// breakdown they sum from.
type Usage struct {
	Statements        int64
	Denied            int64
	Masked            int64
	AuditErrors       int64
	HeartbeatFailures int64
	ByProtocol        map[string]LaneUsage
	// DeniedBy is the process-wide denial count per source in the window.
	// Sources with no denials are omitted. It sums to Denied.
	DeniedBy map[string]int64
}

// Snapshot returns the counts since the previous call and advances the
// baseline. Protocols with no activity in the window are omitted.
func (c *Counters) Snapshot() Usage {
	c.mu.Lock()
	defer c.mu.Unlock()
	u := Usage{
		ByProtocol: make(map[string]LaneUsage, len(c.lanes)),
		DeniedBy:   make(map[string]int64),
	}
	for proto, l := range c.lanes {
		s, m, a := l.statements.Load(), l.masked.Load(), l.auditErrors.Load()
		lu := LaneUsage{
			Statements: s - l.prevStatements,
			Masked:     m - l.prevMasked,
		}
		u.AuditErrors += a - l.prevAuditErrors
		l.prevStatements, l.prevMasked, l.prevAuditErrors = s, m, a

		// One critical section for the denial total and its breakdown: a
		// denial landing between two separate reads would be in one and
		// not the other.
		l.mu.Lock()
		lu.Denied = l.denied - l.prevDenied
		l.prevDenied = l.denied
		for src, n := range l.deniedBy {
			if delta := n - l.prevDeniedBy[src]; delta > 0 {
				u.DeniedBy[src] += delta
			}
			l.prevDeniedBy[src] = n
		}
		l.mu.Unlock()

		if lu == (LaneUsage{}) {
			continue
		}
		u.ByProtocol[proto] = lu
		u.Statements += lu.Statements
		u.Denied += lu.Denied
		u.Masked += lu.Masked
	}
	hb := c.heartbeatFailures.Load()
	u.HeartbeatFailures = hb - c.prevHeartbeat
	c.prevHeartbeat = hb
	return u
}
