package analyzer

import "time"

// SetReviewPacing shortens a hold's wait. The production values are
// constants, and this file only exists in a test binary.
func SetReviewPacing(e *Evaluator, wait, poll time.Duration) {
	e.reviewWait, e.reviewPoll = wait, poll
}

// SetRetryPacing shortens the wait between retries.
func SetRetryPacing(e *Evaluator, base, limit time.Duration) {
	e.retryBase, e.retryCap = base, limit
}

// ShareCache makes dst read and write src's verdict cache, as one cache that
// outlived a configuration change would.
func ShareCache(dst, src *Evaluator) {
	dst.cache = src.cache
}
