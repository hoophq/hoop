package analyzer

import "time"

// SetReviewPacing shortens a hold's wait. The production values are
// constants, and this file only exists in a test binary.
func SetReviewPacing(e *Evaluator, wait, poll time.Duration) {
	e.reviewWait, e.reviewPoll = wait, poll
}
