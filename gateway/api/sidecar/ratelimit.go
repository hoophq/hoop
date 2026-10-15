package apisidecar

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// keyedLimiter is a token bucket per key (a client address, a user). It is
// per process: behind N replicas a key gets N times the rate, which still
// turns guessing one code in 20^8 into centuries.
type keyedLimiter struct {
	mu      sync.Mutex
	limit   rate.Limit
	burst   int
	entries map[string]*limiterEntry
}

type limiterEntry struct {
	lim  *rate.Limiter
	seen time.Time
}

// limiterIdle is how long a key is kept after its last request. Long enough
// that a refill is the only way back to a full bucket.
const limiterIdle = 10 * time.Minute

// limiterPruneAt bounds the map: past it, idle keys are dropped on the next
// call.
const limiterPruneAt = 10000

func newKeyedLimiter(perMinute, burst int) *keyedLimiter {
	return &keyedLimiter{
		limit:   rate.Limit(float64(perMinute) / 60),
		burst:   burst,
		entries: map[string]*limiterEntry{},
	}
}

func (k *keyedLimiter) allow(key string) bool {
	now := time.Now()
	k.mu.Lock()
	defer k.mu.Unlock()
	if len(k.entries) >= limiterPruneAt {
		for key, e := range k.entries {
			if now.Sub(e.seen) > limiterIdle {
				delete(k.entries, key)
			}
		}
	}
	e, ok := k.entries[key]
	if !ok {
		e = &limiterEntry{lim: rate.NewLimiter(k.limit, k.burst)}
		k.entries[key] = e
	}
	e.seen = now
	return e.lim.AllowN(now, 1)
}

var (
	// loginStartLimiter bounds the unauthenticated start route per client
	// address, so one caller cannot fill the table every tenant shares.
	loginStartLimiter = newKeyedLimiter(10, 5)
	// loginCodeLimiter bounds reads and decisions on user codes per user. On
	// a multi-tenant plane anyone who signs up is an admin; without it, one
	// could walk the code space and approve another tenant's sidecar.
	loginCodeLimiter = newKeyedLimiter(20, 10)
)
