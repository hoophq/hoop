package analyzer_test

import (
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/analyzer"
)

// A hold replayed at production pacing: fast polls for the first minute, then
// backoff. The claim count is what one waiting statement costs the plane.
func TestAHoldBacksOffAfterTheFirstMinute(t *testing.T) {
	var (
		waited time.Duration
		poll   = analyzer.ReviewPoll
		claims int
	)
	for {
		waited += min(poll, analyzer.ReviewWait-waited)
		claims++
		if waited < analyzer.ReviewPollFast && poll != analyzer.ReviewPoll {
			t.Fatalf("poll is %s at %s; the first minute must stay at %s", poll, waited, analyzer.ReviewPoll)
		}
		if waited >= analyzer.ReviewWait {
			break
		}
		poll = analyzer.NextReviewPoll(poll, waited, analyzer.ReviewPollFast, analyzer.ReviewPollMax)
		if poll > analyzer.ReviewPollMax {
			t.Fatalf("poll is %s, over the %s cap", poll, analyzer.ReviewPollMax)
		}
	}
	if claims != 71 {
		t.Errorf("a full hold made %d claims, want 71 (360 before the backoff)", claims)
	}
}
