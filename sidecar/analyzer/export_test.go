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

// SetHandoffPinClock replaces the clock the hand-off pins read, so a test
// expires a pin without sleeping.
func SetHandoffPinClock(e *Evaluator, now func() time.Time) {
	e.pins.now = now
}

// PendingFilings counts the hand-off filings still marked in flight.
func PendingFilings(e *Evaluator) int {
	e.pins.mu.Lock()
	defer e.pins.mu.Unlock()
	return len(e.pins.filing)
}

// DecodeStrict is decodeStrict, which reads the arguments of a rule call.
var DecodeStrict = decodeStrict
