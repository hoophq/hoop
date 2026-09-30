package analyzer_test

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

// fakeClock is the time a Budget reads, moved by hand.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Unix(1_700_000_000, 0)} }

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// perMinute is 30 calls a minute: one token every two seconds.
var perMinute = analyzer.RateLimit{Calls: 30, Per: time.Minute}

func rateLimited(t *testing.T, budget *analyzer.Budget, rate analyzer.RateLimit,
	edit func(*analyzer.Config)) (*analyzer.Evaluator, *stubProvider) {
	t.Helper()
	p := &stubProvider{level: analyzer.RiskLow}
	cfg := analyzer.Config{
		Rule:      "appdb",
		Provider:  p,
		Trigger:   deleteTrigger(),
		Actions:   analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionBlock},
		RateLimit: rate,
		Budget:    budget,
	}
	if edit != nil {
		edit(&cfg)
	}
	return mustNew(t, cfg), p
}

func status(v policy.Verdict) string { return v.Annotations[analyzer.MetadataAIStatus] }

// The bucket starts full and holds burst: concurrent connections racing for
// the last token must not all get one, or a burst of 5 becomes 5 plus however
// many connections were in flight, the runaway the limit exists to bound.
func TestABurstLetsExactlyBurstCallsThrough(t *testing.T) {
	clock := newFakeClock()
	ev, p := rateLimited(t, analyzer.NewBudget(clock.now),
		analyzer.RateLimit{Calls: 30, Per: time.Minute, Burst: 5}, nil)

	var wg sync.WaitGroup
	var mu sync.Mutex
	got := map[string]int{}
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := status(ev.Evaluate(deleteStatement()))
			mu.Lock()
			got[s]++
			mu.Unlock()
		}()
	}
	wg.Wait()

	if got[analyzer.StatusOK] != 5 || got[analyzer.StatusRateLimited] != 1 {
		t.Errorf("statuses = %v, want 5 ok and 1 rate_limited", got)
	}
	if n := p.calls.Load(); n != 5 {
		t.Errorf("the provider was called %d times, want the burst of 5", n)
	}
}

// The refill is the whole point of a rate over a cap: a throttled lane
// classifies again once the bucket earns a token, and not before.
func TestTheBucketRefillsAtTheRate(t *testing.T) {
	clock := newFakeClock()
	ev, _ := rateLimited(t, analyzer.NewBudget(clock.now),
		analyzer.RateLimit{Calls: 30, Per: time.Minute, Burst: 1}, nil)

	if s := status(ev.Evaluate(deleteStatement())); s != analyzer.StatusOK {
		t.Fatalf("the first call = %q, want ok", s)
	}
	clock.advance(1999 * time.Millisecond)
	if s := status(ev.Evaluate(deleteStatement())); s != analyzer.StatusRateLimited {
		t.Fatalf("a call before the refill = %q, want rate_limited", s)
	}
	clock.advance(time.Millisecond)
	if s := status(ev.Evaluate(deleteStatement())); s != analyzer.StatusOK {
		t.Fatalf("a call after exactly Per/Calls = %q, want ok", s)
	}
	if s := status(ev.Evaluate(deleteStatement())); s != analyzer.StatusRateLimited {
		t.Fatalf("a second call on one refilled token = %q, want rate_limited", s)
	}
}

// A reload builds a new evaluator over the same Budget. It must continue the
// running bucket rather than start a full one, and a lower burst must clamp
// what is left: otherwise every reload hands out a fresh allowance.
func TestAReloadKeepsTheBucketAndALowerBurstClamps(t *testing.T) {
	clock := newFakeClock()
	budget := analyzer.NewBudget(clock.now)

	gen1, _ := rateLimited(t, budget, analyzer.RateLimit{Calls: 30, Per: time.Minute, Burst: 4}, nil)
	for range 4 {
		gen1.Evaluate(deleteStatement())
	}
	gen2, p2 := rateLimited(t, budget, analyzer.RateLimit{Calls: 30, Per: time.Minute, Burst: 4}, nil)
	if s := status(gen2.Evaluate(deleteStatement())); s != analyzer.StatusRateLimited {
		t.Errorf("the rebuilt evaluator = %q, want rate_limited: the reload re-armed the bucket", s)
	}

	// Refill all 4, then a generation with burst 2 may spend 2 of them.
	clock.advance(8 * time.Second)
	gen3, p3 := rateLimited(t, budget, analyzer.RateLimit{Calls: 30, Per: time.Minute, Burst: 2}, nil)
	for range 3 {
		gen3.Evaluate(deleteStatement())
	}
	if n := p2.calls.Load() + p3.calls.Load(); n != 2 {
		t.Errorf("calls after lowering burst to 2 = %d, want 2", n)
	}
}

// Throttling must not spend the lifetime budget. Without the hand-back, a
// lane under a spike would burn max_calls on calls it never made and then be
// off until a restart, the outage the rate limit exists to avoid.
func TestARateRefusalHandsItsLifetimeSlotBack(t *testing.T) {
	clock := newFakeClock()
	ev, p := rateLimited(t, analyzer.NewBudget(clock.now),
		analyzer.RateLimit{Calls: 30, Per: time.Minute, Burst: 1},
		func(c *analyzer.Config) { c.MaxCalls = 2 })

	ev.Evaluate(deleteStatement())
	for range 5 {
		if s := status(ev.Evaluate(deleteStatement())); s != analyzer.StatusRateLimited {
			t.Fatalf("a throttled call = %q, want rate_limited", s)
		}
	}
	clock.advance(2 * time.Second)
	if s := status(ev.Evaluate(deleteStatement())); s != analyzer.StatusOK {
		t.Fatalf("the call after the refill = %q, want ok: throttled calls spent max_calls", s)
	}
	if got := ev.Stats(); got.Calls != 2 || got.RateLimited != 5 || p.calls.Load() != 2 {
		t.Errorf("stats = %+v with %d provider calls, want 2 calls and 5 rate_limited",
			got, p.calls.Load())
	}

	// With both spent, the lifetime one names the outage: it is the one
	// that lasts until a restart.
	if s := status(ev.Evaluate(deleteStatement())); s != analyzer.StatusBudget {
		t.Errorf("a call past max_calls with an empty bucket = %q, want %q", s, analyzer.StatusBudget)
	}
}

// Rate limiting a holding lane denies, as a spent budget does: no level means
// nothing to hold on, and forwarding would open the human gate for every
// statement a spike pushed past the rate. A lane that only blocks keeps
// allowing, so the fail-closed rule stays scoped to lanes that asked for a
// human.
func TestARateLimitDeniesOnlyOnAHoldingLane(t *testing.T) {
	clock := newFakeClock()
	rate := analyzer.RateLimit{Calls: 1, Per: time.Hour}

	rev := &recordingReviewer{res: analyzer.ReviewResult{ID: "9f97", Status: "PENDING"}}
	hold := holdingEvaluator(t, rev, func(c *analyzer.Config) {
		c.RateLimit, c.Budget, c.FailOpen = rate, analyzer.NewBudget(clock.now), true
	})
	hold.Evaluate(deleteStatement())
	v := hold.Evaluate(deleteStatement())
	if !v.Denied {
		t.Fatal("a rate-limited statement was forwarded on a lane that holds")
	}
	if !strings.Contains(v.Message, "rate limit") || status(v) != analyzer.StatusRateLimited {
		t.Errorf("denial = %q with ai_status %q, want it to name the rate limit", v.Message, status(v))
	}

	block, _ := rateLimited(t, analyzer.NewBudget(clock.now), rate, nil)
	block.Evaluate(deleteStatement())
	if v := block.Evaluate(deleteStatement()); v.Denied || status(v) != analyzer.StatusRateLimited {
		t.Errorf("a rate-limited lane that only blocks: denied %v, ai_status %q; want allowed",
			v.Denied, status(v))
	}
}

// The log hears the two edges of an episode, not every refusal, and the
// closing edge carries how many statements went unclassified.
func TestOnRateLimitReportsTheEdgesOfAnEpisode(t *testing.T) {
	clock := newFakeClock()
	type event struct {
		limited bool
		refused int64
	}
	var events []event
	ev, _ := rateLimited(t, analyzer.NewBudget(clock.now),
		analyzer.RateLimit{Calls: 1, Per: time.Minute},
		func(c *analyzer.Config) {
			c.OnRateLimit = func(limited bool, refused int64) {
				events = append(events, event{limited, refused})
			}
		})

	for range 4 {
		ev.Evaluate(deleteStatement())
	}
	clock.advance(time.Minute)
	ev.Evaluate(deleteStatement())

	want := []event{{true, 1}, {false, 3}}
	if len(events) != len(want) || events[0] != want[0] || events[1] != want[1] {
		t.Errorf("events = %+v, want %+v", events, want)
	}
}

// A half-set rate loads and bounds nothing, so the library refuses it rather
// than running unlimited under a config that reads as limited.
func TestAHalfSetRateLimitIsRefused(t *testing.T) {
	for _, rate := range []analyzer.RateLimit{
		{Calls: 30},
		{Per: time.Minute},
		{Burst: 5},
		{Calls: -1, Per: time.Minute},
	} {
		_, err := analyzer.New(analyzer.Config{Provider: &stubProvider{}, RateLimit: rate})
		if err == nil {
			t.Errorf("rate %+v was accepted", rate)
		}
	}
	if _, err := analyzer.New(analyzer.Config{Provider: &stubProvider{}, RateLimit: perMinute}); err != nil {
		t.Errorf("a whole rate was refused: %v", err)
	}
}

// Cache hits draw no token: the cache already costs nothing, and a lane
// repeating one statement shape must not be throttled by its own hits.
func TestACacheHitDrawsNoToken(t *testing.T) {
	clock := newFakeClock()
	ev, p := rateLimited(t, analyzer.NewBudget(clock.now),
		analyzer.RateLimit{Calls: 1, Per: time.Hour},
		func(c *analyzer.Config) { c.CacheSize, c.CacheTTL = 8, time.Hour })

	for range 5 {
		if s := status(ev.Evaluate(sqlStmt("DELETE FROM t", inspect.OpDelete, "t"))); s == analyzer.StatusRateLimited {
			t.Fatal("a cached statement was rate limited")
		}
	}
	if n := p.calls.Load(); n != 1 {
		t.Errorf("provider calls = %d, want 1", n)
	}
}

// A throttled call must never touch the lifetime count, even for a moment:
// a concurrent call reading the count in that window would report a spent
// max_calls that is not spent, and deny on a lane that holds.
func TestThrottledCallsNeverReportAFalseBudgetExhausted(t *testing.T) {
	clock := newFakeClock()
	ev, _ := rateLimited(t, analyzer.NewBudget(clock.now),
		analyzer.RateLimit{Calls: 1, Per: time.Hour},
		func(c *analyzer.Config) { c.MaxCalls = 2 })
	ev.Evaluate(deleteStatement()) // spends the only token; one call of two made

	var wg sync.WaitGroup
	var mu sync.Mutex
	got := map[string]int{}
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				s := status(ev.Evaluate(deleteStatement()))
				mu.Lock()
				got[s]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if got[analyzer.StatusBudget] != 0 || got[analyzer.StatusRateLimited] != 64*50 {
		t.Errorf("statuses = %v, want only rate_limited: one of two calls was made", got)
	}
}

// A reload that removes the rate must close a running episode, or the log
// says "reached" and never "cleared". Removing it must not refill the bucket
// either: a limit put back before it refilled starts a new episode at once.
func TestRemovingTheRateClosesTheEpisodeWithoutRefilling(t *testing.T) {
	clock := newFakeClock()
	budget := analyzer.NewBudget(clock.now)
	type event struct {
		gen     int
		limited bool
		refused int64
	}
	var events []event
	build := func(gen int, rate analyzer.RateLimit) *analyzer.Evaluator {
		ev, _ := rateLimited(t, budget, rate, func(c *analyzer.Config) {
			c.OnRateLimit = func(limited bool, refused int64) {
				events = append(events, event{gen, limited, refused})
			}
		})
		return ev
	}
	perMin := analyzer.RateLimit{Calls: 1, Per: time.Minute}

	gen1 := build(1, perMin)
	gen1.Evaluate(deleteStatement())
	gen1.Evaluate(deleteStatement()) // refused: the episode starts

	gen2 := build(2, analyzer.RateLimit{})
	if s := status(gen2.Evaluate(deleteStatement())); s != analyzer.StatusOK {
		t.Fatalf("a lane with the rate removed = %q, want ok", s)
	}

	gen3 := build(3, perMin)
	if s := status(gen3.Evaluate(deleteStatement())); s != analyzer.StatusRateLimited {
		t.Errorf("the rate put back = %q, want rate_limited: removing it refilled the bucket", s)
	}

	want := []event{{1, true, 1}, {2, false, 1}, {3, true, 1}}
	if len(events) != len(want) {
		t.Fatalf("events = %+v, want %+v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Errorf("events = %+v, want %+v", events, want)
			break
		}
	}
}

// ai_status on the audit record folds like the finding: a second analyzer
// that answered must not overwrite the first one's throttling, or the trail
// reads "ok" while the policy was told "unavailable".
func TestAThrottledStatusSurvivesALaterAnalyzerThatAnswered(t *testing.T) {
	clock := newFakeClock()
	throttled := func(t *testing.T) *analyzer.Evaluator {
		ev, _ := rateLimited(t, analyzer.NewBudget(clock.now),
			analyzer.RateLimit{Calls: 1, Per: time.Hour}, nil)
		ev.Evaluate(deleteStatement()) // spends the only token
		return ev
	}
	working := func(t *testing.T) *analyzer.Evaluator {
		ev, _ := rateLimited(t, nil, analyzer.RateLimit{}, nil)
		return ev
	}
	for _, tc := range []struct {
		name          string
		first, second func(*testing.T) *analyzer.Evaluator
	}{
		{"throttled first", throttled, working},
		{"throttled second", working, throttled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := policy.Chain{tc.first(t), tc.second(t)}.Evaluate(deleteStatement())
			if got := status(v); got != analyzer.StatusRateLimited {
				t.Errorf("ai_status = %q, want %q", got, analyzer.StatusRateLimited)
			}
		})
	}
}
