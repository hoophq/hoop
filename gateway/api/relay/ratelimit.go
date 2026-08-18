package relayapi

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

// sweepLocked drops idle buckets, and clears the map when that frees nothing.
//
// Clearing is safe here in a way it would not be for a security control:
// every bucket lost is a caller handed a full burst, which is a rate-limit
// reset rather than a bypass, and reaching the cap at all means something
// pathological is already happening.
func (l *pollLimiter) sweepLocked(now time.Time) {
	for k, b := range l.buckets {
		if now.Sub(b.last) > pollBucketIdle {
			delete(l.buckets, k)
		}
	}
	if len(l.buckets) >= pollBucketCap {
		clear(l.buckets)
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
