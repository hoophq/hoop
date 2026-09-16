package analytics

import (
	"sync"
	"sync/atomic"
)

// Counters accumulates the data-path facts a usage event reports. One per
// process; every connection's gate writes into the LaneCounter for its
// protocol, and Snapshot reads the deltas since the previous snapshot.
//
// Lock-free on the write side: a gate increments an atomic per statement,
// and nothing here is allowed to cost the data path more than that.
type Counters struct {
	mu    sync.Mutex
	lanes map[string]*LaneCounter

	heartbeatFailures atomic.Int64

	prevHeartbeat int64
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
		lc = &LaneCounter{}
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
	statements atomic.Int64
	denied     atomic.Int64
	masked     atomic.Int64

	// prev* hold the values the last Snapshot reported, so the next one
	// reports a delta. Read and written under Counters.mu only.
	prevStatements, prevDenied, prevMasked int64
}

// Statement records one judged statement.
func (l *LaneCounter) Statement(denied bool) {
	l.statements.Add(1)
	if denied {
		l.denied.Add(1)
	}
}

// Masked records values rewritten out of one response.
func (l *LaneCounter) Masked(values int) {
	if values > 0 {
		l.masked.Add(int64(values))
	}
}

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
	HeartbeatFailures int64
	ByProtocol        map[string]LaneUsage
}

// Snapshot returns the counts since the previous call and advances the
// baseline. Protocols with no activity in the window are omitted.
func (c *Counters) Snapshot() Usage {
	c.mu.Lock()
	defer c.mu.Unlock()
	u := Usage{ByProtocol: make(map[string]LaneUsage, len(c.lanes))}
	for proto, l := range c.lanes {
		s, d, m := l.statements.Load(), l.denied.Load(), l.masked.Load()
		lu := LaneUsage{
			Statements: s - l.prevStatements,
			Denied:     d - l.prevDenied,
			Masked:     m - l.prevMasked,
		}
		l.prevStatements, l.prevDenied, l.prevMasked = s, d, m
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
