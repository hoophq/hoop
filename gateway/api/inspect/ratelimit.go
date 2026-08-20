package inspectapi

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// The poll endpoint is the one route in this package a well-behaved caller
// hits repeatedly and a badly-behaved one hits in a hot loop: an agent waiting
// on a human approval has nothing else to do. Each poll is a database query,
// so the loop is a self-inflicted denial of service against the gateway.
//
// Rate limiting it is not optional. The claim and create routes are
// deliberately NOT limited: they sit on the data path, and refusing one of
// those refuses a statement.
const (
	// pollRatePerSec is the sustained budget per sandbox. A human approval
	// takes minutes; one poll a second is already far more attentive than
	// the thing being waited on.
	pollRatePerSec = 1.0

	// pollBurst absorbs a retry cluster — a few connections in one agent
	// noticing a refusal at once — without touching the sustained rate.
	pollBurst = 10.0

	// pollBucketIdle is how long an idle bucket is kept. A bucket that has
	// been idle this long has refilled to full, so dropping it loses
	// nothing.
	pollBucketIdle = 5 * time.Minute

	// pollBucketCap bounds the map. One entry per (org, sandbox) pair, so
	// this is far above any real deployment and exists only so a stream of
	// distinct credentials cannot grow it without limit.
	pollBucketCap = 4096
)

// pollLimiter is a token bucket per (org, sandbox).
//
// Per REPLICA, deliberately, and worth stating plainly: a gateway running four
// replicas allows four times this rate in the worst case. A shared counter
// would need Redis or a table write per poll, which costs more than the query
// it is protecting. Bounding each replica bounds the total by a known factor,
// which is what this control is for.
type pollLimiter struct {
	mu      sync.Mutex
	buckets map[string]*pollBucket

	// lastSweep throttles the full idle pass. Without it, a map held at the
	// cap would walk every entry on every new key, under this mutex, which
	// is exactly the workload a stream of distinct credentials produces.
	lastSweep time.Time
}

type pollBucket struct {
	tokens float64
	last   time.Time
}

var polls = &pollLimiter{buckets: make(map[string]*pollBucket)}

// allow reports whether this caller may spend one request now.
func (l *pollLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= pollBucketCap {
			l.sweepLocked(now)
		}
		// Sweeping frees only buckets that are genuinely idle. If the map is
		// still full every entry is an active caller, so one has to go —
		// exactly one, chosen among a bounded sample.
		if len(l.buckets) >= pollBucketCap {
			l.evictOneLocked()
		}
		l.buckets[key] = &pollBucket{tokens: pollBurst - 1, last: now}
		return true
	}

	b.tokens += now.Sub(b.last).Seconds() * pollRatePerSec
	if b.tokens > pollBurst {
		b.tokens = pollBurst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// sweepLocked drops buckets that have been idle long enough to have refilled.
//
// Dropping those costs nothing: a bucket idle for pollBucketIdle is already
// back at pollBurst, so re-creating it produces the same state.
//
// It does NOT clear the map when that frees nothing. Clearing would hand every
// active caller a full burst because one unrelated caller happened to arrive
// at the cap — a global rate-limit reset triggered by whoever showed up, and a
// lever an abuser cycling credentials could pull deliberately. Rate-limit
// state for an active credential survives anything another caller does.
//
// Throttled to one pass per pollBucketIdle. A pass is O(len(buckets)) under
// the mutex, and running it on every new key while the map sits at the cap
// would make the limiter the bottleneck instead of the thing it protects.
func (l *pollLimiter) sweepLocked(now time.Time) {
	if now.Sub(l.lastSweep) < pollBucketIdle {
		return
	}
	l.lastSweep = now
	for k, b := range l.buckets {
		if now.Sub(b.last) > pollBucketIdle {
			delete(l.buckets, k)
		}
	}
}

// evictOneLocked frees exactly one slot, dropping the least recently used
// bucket among a bounded random sample.
//
// One victim rather than all of them, and the cheapest possible one: a bucket
// is chosen by how long it has been idle, and idle time is what refills it, so
// the bucket evicted is the one closest to being full. What that caller loses
// is the fraction of a burst it had not yet earned back.
//
// The sample is bounded, so the work per allocation is constant no matter how
// large the map is. Go randomizes map iteration order, which is what makes the
// first few entries a sample rather than a scan of the same corner every time.
// This is approximate LRU on purpose — an exact one needs a heap or an
// intrusive list to maintain on the hot path, to pick a marginally better
// victim from a set that is entirely active callers anyway.
func (l *pollLimiter) evictOneLocked() {
	const sample = 8

	var victim string
	var oldest time.Time
	seen := 0
	for k, b := range l.buckets {
		if seen == 0 || b.last.Before(oldest) {
			victim, oldest = k, b.last
		}
		if seen++; seen == sample {
			break
		}
	}
	if seen > 0 {
		delete(l.buckets, victim)
	}
}

// RateLimitPollMiddleware bounds how often one sandbox may poll for approval.
//
// Keyed on the authenticated credential, never on a request field: keying on
// anything the caller supplies would let it reset its own budget by varying
// that field.
func RateLimitPollMiddleware(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	if ctx == nil {
		// Unauthenticated requests never reach here — AuthMiddleware runs
		// first — so this is a wiring mistake rather than a caller's doing.
		// Refuse rather than let an unkeyed request through unlimited.
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "access denied"})
		return
	}
	if !polls.allow(ctx.OrgID+"\x00"+ctx.UserID, time.Now()) {
		c.Header("Retry-After", "1")
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
			"message": "polling too fast; wait for the review and retry"})
		return
	}
	c.Next()
}
