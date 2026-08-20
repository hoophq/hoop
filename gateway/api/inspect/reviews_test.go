package inspectapi

import (
	"strings"
	"sync"
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

// The regression this policy exists for: a flood of new credentials must not
// hand active callers back their spent budget.
//
// The map used to be cleared once an idle sweep freed nothing, so one caller
// arriving at the cap reset every other caller — and an abuser cycling
// credentials could pull that lever on demand.
//
// Measured as polls GRANTED to already-throttled callers, because that is the
// budget leak itself. Both policies earn the same natural refill from the
// clock advancing; anything beyond it came from an eviction.
func TestPollLimiterDoesNotHandBackBudgetWhenTheMapFills(t *testing.T) {
	l := &pollLimiter{buckets: make(map[string]*pollBucket)}
	base := time.Now()

	const callers = 50
	key := func(c int) string { return "caller" + string(rune(c)) + "\x00s" }

	for c := range callers {
		for range int(pollBurst) {
			l.allow(key(c), base)
		}
		if l.allow(key(c), base) {
			t.Fatalf("precondition: caller %d should be out of tokens", c)
		}
	}

	// The callers keep retrying throughout, which is what a caller waiting on
	// a human does. Each retry is refused but keeps the bucket active.
	const floodSize = pollBucketCap + 2000
	granted := 0
	for i := range floodSize {
		at := base.Add(time.Duration(i) * time.Millisecond)
		l.allow("flood"+string(rune(i))+"\x00s", at)
		if i%50 == 0 {
			for c := range callers {
				if l.allow(key(c), at) {
					granted++
				}
			}
		}
	}

	// What the clock alone owes them: the flood spans floodSize ms, refilling
	// at pollRatePerSec, capped at a full burst.
	refill := float64(floodSize) / 1000 * pollRatePerSec
	if refill > pollBurst {
		refill = pollBurst
	}
	natural := int(refill) * callers
	// Slack for rounding across 50 independent buckets. A global reset costs
	// a full burst per caller per clear, which is far outside this.
	limit := natural + callers
	if granted > limit {
		t.Errorf("throttled callers were granted %d polls; the clock owes them about %d "+
			"(limit %d) — the rest was budget handed back by eviction",
			granted, natural, limit)
	}
}

// Bounded, and bounded by evicting rather than by refusing to serve: a new
// credential still works when the map is at the cap.
func TestPollLimiterStaysBoundedAndStillServesNewCallers(t *testing.T) {
	l := &pollLimiter{buckets: make(map[string]*pollBucket)}
	now := time.Now()

	for i := range pollBucketCap + 200 {
		if !l.allow("k"+string(rune(i))+"\x00s", now) {
			t.Fatalf("a new credential was refused at request %d; eviction should have made room", i)
		}
	}
	if len(l.buckets) > pollBucketCap {
		t.Fatalf("buckets = %d, cap is %d", len(l.buckets), pollBucketCap)
	}
}

// An idle bucket has refilled to full, so dropping it loses nothing. The sweep
// is what keeps the map from reaching the cap on ordinary churn.
func TestPollLimiterSweepsIdleBuckets(t *testing.T) {
	l := &pollLimiter{buckets: make(map[string]*pollBucket)}
	now := time.Now()

	for i := range pollBucketCap {
		l.allow("old"+string(rune(i))+"\x00s", now)
	}
	// Long enough that every bucket above is idle, and past the sweep
	// throttle so the pass actually runs.
	later := now.Add(pollBucketIdle * 2)
	l.allow("fresh\x00s", later)

	if len(l.buckets) > 2 {
		t.Errorf("buckets = %d; the idle ones were not swept", len(l.buckets))
	}
}

// The sweep is throttled, so the work done per allocation stays bounded when
// the map sits at the cap. Eviction is what makes room in between.
func TestPollLimiterSweepIsThrottled(t *testing.T) {
	l := &pollLimiter{buckets: make(map[string]*pollBucket)}
	now := time.Now()

	l.allow("a\x00s", now)
	first := l.lastSweep

	// A second allocation moments later must not trigger another pass.
	for i := range pollBucketCap + 10 {
		l.allow("b"+string(rune(i))+"\x00s", now.Add(time.Second))
	}
	if !l.lastSweep.Equal(first) && l.lastSweep.Sub(first) < pollBucketIdle {
		t.Errorf("swept again after %v, throttle is %v", l.lastSweep.Sub(first), pollBucketIdle)
	}
	if len(l.buckets) > pollBucketCap {
		t.Fatalf("buckets = %d, cap is %d — eviction did not cover the throttled sweep",
			len(l.buckets), pollBucketCap)
	}
}

// The limiter is shared across every in-flight poll on the replica.
func TestPollLimiterIsRaceFree(t *testing.T) {
	l := &pollLimiter{buckets: make(map[string]*pollBucket)}
	now := time.Now()

	var wg sync.WaitGroup
	for w := range 16 {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := range 500 {
				// A mix of repeat callers and fresh keys, so allocation,
				// eviction and the sweep all run under contention.
				l.allow("w"+string(rune(w))+"\x00"+string(rune(i%64)), now.Add(time.Duration(i)*time.Second))
			}
		}(w)
	}
	wg.Wait()

	if len(l.buckets) > pollBucketCap {
		t.Fatalf("buckets = %d, cap is %d", len(l.buckets), pollBucketCap)
	}
}

// The attack this exists to stop: submit benign text for a human to read and
// the hash of something else as the key. A person approves what they saw, and
// the claim — which presents the hash of the bytes actually on the wire —
// finds an approval waiting for a statement nobody reviewed.
//
// Format validation cannot catch it. sha256("DROP TABLE users") is a perfectly
// well-formed hash; the question is what it is a hash OF.
func TestStatementHashMustBeTheHashOfTheDisplayedStatement(t *testing.T) {
	const shown = "SELECT 1"
	const actual = "DROP TABLE users"

	forged := hashOf(actual)
	if !validStatementHash(forged) {
		t.Fatal("premise check: the forged hash is well-formed, which is the whole problem")
	}
	if statementHashMatches(shown, forged) {
		t.Fatal("a hash of different SQL was accepted for the displayed statement")
	}
	if !statementHashMatches(shown, hashOf(shown)) {
		t.Error("an honest request was refused")
	}

	// Byte-exact, because the claim is. Anything the gate does not treat as
	// the same statement must not pass as the same statement here either.
	for _, tampered := range []string{
		shown + " ",
		" " + shown,
		"select 1",
		shown + ";",
		shown + "\n",
	} {
		if statementHashMatches(tampered, hashOf(shown)) {
			t.Errorf("%q passed as the preimage of %q", tampered, shown)
		}
	}
}

// The gateway has to agree with the relay about what the hash covers, or an
// honest relay gets 400s. This pins the exact construction: lowercase hex
// SHA-256 over the canonical text, with no length prefix, no salt and no
// normalization of any kind.
func TestHashOfMatchesTheRelayConstruction(t *testing.T) {
	// Pinned against an external reference, not against ourselves:
	//   printf '%s' 'SELECT 1' | shasum -a 256
	// A round-trip test would pass even if the construction changed on both
	// sides at once, which is exactly the drift that would silently stop the
	// relay's hashes from matching the gateway's.
	const want = "e004ebd5b5532a4b85984a62f8ad48a81aa3460c1ca07701f386135d72cdecf5"
	got := hashOf("SELECT 1")

	if got != want {
		t.Fatalf("hashOf(%q) = %q, want %q — the construction drifted from plain SHA-256",
			"SELECT 1", got, want)
	}
	if got != strings.ToLower(got) {
		t.Errorf("hash is not lowercase: %q", got)
	}
	if !validStatementHash(got) {
		t.Errorf("our own hash fails our own shape check: %q", got)
	}
	// An HTTP statement is method+URI, a blank line, then the body — the
	// relay's canonical form for that protocol. Nothing here may special-case
	// it; it is just text.
	httpCanonical := "POST /anything/users/12345/orders\n\n{\"action\":\"purge\"}"
	if !statementHashMatches(httpCanonical, hashOf(httpCanonical)) {
		t.Error("an HTTP canonical statement did not round-trip")
	}
}
