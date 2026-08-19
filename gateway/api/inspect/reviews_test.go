package inspectapi

import (
	"strings"
	"testing"
	"time"
)

// The hash is a lookup key on an indexed column reached by a machine
// credential. Enforcing its exact shape keeps a caller from putting an
// unbounded or wildcard-ish string there, and turns a malformed key into a
// visible 400 rather than a silent miss that reads as "no approval".
func TestValidStatementHash(t *testing.T) {
	good := "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
	if !validStatementHash(good) {
		t.Fatal("a real SHA-256 was rejected")
	}

	bad := map[string]string{
		"":                                "empty",
		strings.Repeat("a", 63):           "too short",
		strings.Repeat("a", 65):           "too long",
		strings.ToUpper(good):             "uppercase",
		strings.Repeat("g", 64):           "non-hex",
		strings.Repeat("a", 60) + "%25' ": "an escaping attempt",
	}
	for in, why := range bad {
		if validStatementHash(in) {
			t.Errorf("accepted %s: %q", why, in)
		}
	}
}

func TestPollLimiterSpendsItsBurstThenRefuses(t *testing.T) {
	l := &pollLimiter{buckets: make(map[string]*pollBucket)}
	now := time.Now()

	for i := range int(pollBurst) {
		if !l.allow("org\x00sandbox", now) {
			t.Fatalf("refused request %d, inside the burst of %v", i+1, pollBurst)
		}
	}
	if l.allow("org\x00sandbox", now) {
		t.Fatal("the burst did not bound anything")
	}

	// Refills at the sustained rate, so a patient caller is never stuck.
	if !l.allow("org\x00sandbox", now.Add(time.Second)) {
		t.Error("a second later the bucket had not refilled")
	}
}

// The budget is per credential. One noisy sandbox must not throttle another,
// and — the part that matters — a caller must not be able to reset its own
// budget, which is why the key is the authenticated identity and nothing the
// request carries.
func TestPollLimiterIsPerCaller(t *testing.T) {
	l := &pollLimiter{buckets: make(map[string]*pollBucket)}
	now := time.Now()

	for range int(pollBurst) {
		l.allow("org\x00noisy", now)
	}
	if l.allow("org\x00noisy", now) {
		t.Fatal("the noisy caller was not throttled")
	}
	if !l.allow("org\x00quiet", now) {
		t.Error("a different sandbox was throttled by someone else's traffic")
	}
}

// The bucket map is bounded, or a stream of distinct credentials grows it
// without limit.
func TestPollLimiterEvicts(t *testing.T) {
	l := &pollLimiter{buckets: make(map[string]*pollBucket)}
	now := time.Now()

	for i := range pollBucketCap + 50 {
		l.allow(string(rune(i))+"\x00k", now)
	}
	if len(l.buckets) > pollBucketCap {
		t.Fatalf("buckets = %d, cap is %d", len(l.buckets), pollBucketCap)
	}
}
