package analyzer

import "time"

// SetReviewPacing shortens a hold's wait. The production values are
// constants, and this file only exists in a test binary.
func SetReviewPacing(e *Evaluator, wait, poll time.Duration) {
	e.reviewWait, e.reviewPoll = wait, poll
}

// NextReviewPoll and the production pacing, so a test can replay a whole
// hold without waiting it out.
var NextReviewPoll = nextReviewPoll

const (
	ReviewPoll     = reviewPoll
	ReviewPollFast = reviewPollFast
	ReviewPollMax  = reviewPollMax
)
