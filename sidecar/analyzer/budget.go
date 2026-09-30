package analyzer

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// RateLimit bounds how fast a Budget is spent: Calls classifications per
// Per, drawn from a bucket that holds at most Burst.
//
// It sits beside MaxCalls rather than replacing it. MaxCalls is the total a
// process may ever spend and it never refills, so once a long-running relay
// reaches it on ordinary traffic the analyzer stays off until a restart. A
// rate refills: a spike is throttled, and classification resumes once the
// spike passes.
type RateLimit struct {
	// Calls and Per are the refill rate. Both zero is no limit; one
	// without the other is refused by New, because a rate with no period
	// (or a period with no rate) bounds nothing while looking set.
	Calls int
	Per   time.Duration

	// Burst is the bucket's capacity: how many calls may go out back to
	// back after a quiet period. Zero means Calls, so "30 per minute"
	// sends 30 at once and then one every two seconds.
	Burst int
}

// Enabled reports whether the limit bounds anything.
func (r RateLimit) Enabled() bool { return r.Calls > 0 && r.Per > 0 }

// Capacity is the effective burst: Burst, or Calls when Burst is zero.
func (r RateLimit) Capacity() int {
	if r.Burst > 0 {
		return r.Burst
	}
	return r.Calls
}

func (r RateLimit) validate() error {
	switch {
	case r.Calls < 0 || r.Per < 0 || r.Burst < 0:
		return errors.New("sidecar/analyzer: a rate limit value is negative")
	case (r.Calls > 0) != (r.Per > 0):
		return errors.New("sidecar/analyzer: a rate limit needs both calls and a period")
	case r.Burst > 0 && !r.Enabled():
		return errors.New("sidecar/analyzer: a rate limit burst needs calls and a period")
	}
	return nil
}

// Budget is what an analyzer spends from: the lifetime call count MaxCalls
// bounds, and the token bucket a RateLimit draws on.
//
// One Budget is handed to every generation of one lane's evaluator, so a
// hot reload neither re-arms the lifetime count nor refills the bucket: the
// draining generation and its replacement pay from one purse.
//
// The rate is NOT stored here. Each evaluator draws with the RateLimit it
// was built with, the same way each one compares the shared counter with its
// own MaxCalls. Storing it would mean a reload writes the purse while it
// builds its lanes, and the build runs before the reload's own refusals: a
// document refused after the build would already have changed the rate the
// running lanes spend at.
//
// The zero value is ready to use and reads the wall clock.
type Budget struct {
	calls atomic.Int64

	now func() time.Time

	mu     sync.Mutex
	tokens float64
	// last is zero until the first draw, which is what starts the bucket
	// full: a lane that has spent nothing has its whole burst available.
	last time.Time
	// limited is true between the first refused draw and the next granted
	// one. The log reports those two edges, not every refusal in between.
	limited bool
	refused int64
}

// NewBudget returns a Budget that reads now for the time. Nil reads the wall
// clock, which is what the zero value does.
func NewBudget(now func() time.Time) *Budget { return &Budget{now: now} }

// rateEdge names a change in whether a Budget is refusing calls.
type rateEdge int

const (
	edgeNone rateEdge = iota
	// edgeStarted: this call was the first one the rate refused.
	edgeStarted
	// edgeEnded: this call was the first one granted after a refusal.
	edgeEnded
)

// take decides one call against maxCalls and r, and spends from the budget
// only when both grant it. It returns "" for a granted call, StatusBudget or
// StatusRateLimited for a refused one, and whether this call started or
// ended a rate-limited episode, with how many calls the episode refused.
//
// Both checks run under one lock, and the lifetime count moves only for a
// granted call. Reserving the slot first and handing it back on a rate
// refusal would leave a window where a concurrent call reads the inflated
// count and reports max_calls spent when it is not, which on a holding lane
// is a denial.
//
// The lifetime check runs first, so a spent max_calls reports
// budget_exhausted even when the bucket is empty too: that one lasts until
// a restart, which is the more useful thing to say. A call with no rate
// closes a running episode without touching the bucket, so a reload that
// removes the limit logs the clearing edge, and one that puts it back finds
// the bucket where it was left.
func (b *Budget) take(maxCalls int, r RateLimit) (status string, edge rateEdge, refused int64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if maxCalls > 0 && b.calls.Load() >= int64(maxCalls) {
		return StatusBudget, edgeNone, 0
	}
	if !r.Enabled() {
		edge, refused = b.endEpisode()
		b.calls.Add(1)
		return "", edge, refused
	}

	now := time.Now()
	if b.now != nil {
		now = b.now()
	}
	capacity := float64(r.Capacity())
	switch {
	case b.last.IsZero():
		b.tokens = capacity
		b.last = now
	case now.After(b.last):
		// Nanosecond arithmetic, so a whole number of refill periods
		// earns a whole number of tokens: 2s at 30 per minute is exactly
		// one, never 0.9999 and a refusal.
		b.tokens += float64(now.Sub(b.last)) * float64(r.Calls) / float64(r.Per)
		b.last = now
	}
	// Clamped on every draw with the drawing evaluator's capacity. A
	// reload that lowered burst takes effect at its first draw, and one
	// that raised it grants nothing until the refill earns it: a reload
	// never hands out a fresh allowance.
	b.tokens = min(b.tokens, capacity)

	if b.tokens < 1 {
		b.refused++
		if b.limited {
			return StatusRateLimited, edgeNone, b.refused
		}
		b.limited = true
		return StatusRateLimited, edgeStarted, b.refused
	}
	b.tokens--
	edge, refused = b.endEpisode()
	b.calls.Add(1)
	return "", edge, refused
}

// endEpisode closes a running rate-limited episode. Called with b.mu held.
func (b *Budget) endEpisode() (rateEdge, int64) {
	if !b.limited {
		return edgeNone, 0
	}
	refused := b.refused
	b.limited, b.refused = false, 0
	return edgeEnded, refused
}
