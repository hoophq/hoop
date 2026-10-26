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

// exhausted is whether key has no token left now, without spending one.
func (k *keyedLimiter) exhausted(key string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	e, ok := k.entries[key]
	return ok && e.lim.TokensAt(time.Now()) < 1
}

var (
	// No limiter is keyed by client address: the engine trusts no proxy, so
	// behind an Ingress every caller has the proxy's address.
	//
	// loginStartLimiter is the whole plane's budget of the unauthenticated
	// start route. maxPendingSidecarLogins bounds the table it fills.
	loginStartLimiter = newKeyedLimiter(300, 100)
	// loginCodeLimiter bounds reads and decisions on user codes per user.
	loginCodeLimiter = newKeyedLimiter(20, 10)
	// loginMissLimiter is the whole plane's budget of wrong codes. Spent, code
	// lookups refuse until it refills for every user that sent a wrong code
	// recently (loginMissers), however many accounts a guesser holds: at 600
	// a minute, hitting one live code in 20^8 takes decades. Users with no
	// recent miss are not refused, so guessers cannot block other orgs.
	loginMissLimiter = newKeyedLimiter(600, 600)
	loginMissers     = newRecentKeys(limiterIdle)
)

// loginMissKey and loginStartKey are the one key of their plane-wide limiter.
const (
	loginMissKey  = "plane"
	loginStartKey = "plane"
)

// recentKeys remembers which keys (users) did something within a window:
// here, sent a wrong code.
type recentKeys struct {
	mu     sync.Mutex
	window time.Duration
	seen   map[string]time.Time
}

func newRecentKeys(window time.Duration) *recentKeys {
	return &recentKeys{window: window, seen: map[string]time.Time{}}
}

func (r *recentKeys) mark(keys ...string) {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.seen) >= limiterPruneAt {
		for key, at := range r.seen {
			if now.Sub(at) > r.window {
				delete(r.seen, key)
			}
		}
	}
	for _, key := range keys {
		r.seen[key] = now
	}
}

// any is whether one of keys was marked within the window.
func (r *recentKeys) any(keys ...string) bool {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, key := range keys {
		if at, ok := r.seen[key]; ok && now.Sub(at) <= r.window {
			return true
		}
	}
	return false
}
