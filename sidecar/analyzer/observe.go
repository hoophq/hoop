package analyzer

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"
)

// CallOutcome is how one classification's provider call ended.
type CallOutcome string

const (
	// CallOK means the provider answered with a usable risk level.
	CallOK CallOutcome = "ok"

	// CallError means the provider answered with an error, or with
	// nothing the evaluator could use, inside the deadline.
	CallError CallOutcome = "error"

	// CallTimeout means the deadline (Config.Timeout) ended the call.
	CallTimeout CallOutcome = "timeout"
)

// Call describes one classification sent to the provider: every attempt,
// retries included, under one deadline.
type Call struct {
	// Rule is the evaluator's name: the lane for an analyzer block, the
	// rule name for the deprecated rule form.
	Rule string

	Outcome CallOutcome

	// Duration runs from the first attempt to the last answer. A timeout
	// reports about Timeout.
	Duration time.Duration
	Timeout  time.Duration

	// Attempts is 1 plus the retries made.
	Attempts int

	// Err is the final error; nil when Outcome is CallOK.
	Err error
}

// Outcome is what the evaluator did with one eligible statement.
type Outcome struct {
	Rule string

	// Status is one of the Status* values.
	Status string

	// RiskLevel and Action are set for a classified statement (ok, cached)
	// and empty otherwise. A refused statement carries ActionBlock.
	RiskLevel RiskLevel
	Action    Action
}

// Retry pacing. The first retry waits about retryBase, each next one twice
// as long, never more than retryCap. A provider's Retry-After replaces the
// computed wait.
const (
	retryBase = 250 * time.Millisecond
	retryCap  = 4 * time.Second
)

// call sends one classification, retrying a retryable provider answer while
// the deadline allows, and reports it through OnCall.
//
// A retry happens only when the remaining time covers the wait. When it does
// not, the provider's own answer is returned, not a timeout: "503 Service
// Unavailable" tells the operator what happened, a deadline that expired
// while nothing was in flight does not.
func (e *Evaluator) call(ctx context.Context, text string) (*Result, error) {
	start := time.Now()
	attempts := 0
	var res *Result
	var err error
	for {
		attempts++
		res, err = e.cfg.Provider.Classify(ctx, e.prompt, text)
		if err == nil || attempts > e.cfg.MaxRetries {
			break
		}
		wait, ok := e.retryWait(ctx, attempts, err)
		if !ok {
			break
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
		case <-t.C:
		}
		if ctx.Err() != nil {
			break
		}
	}
	if err == nil && (res == nil || !res.RiskLevel.Valid()) {
		err = errors.New("provider returned no usable risk level")
	}

	c := Call{
		Rule:     e.cfg.Rule,
		Outcome:  CallOK,
		Duration: time.Since(start),
		Timeout:  e.cfg.Timeout,
		Attempts: attempts,
	}
	if err != nil {
		c.Outcome = CallError
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			c.Outcome = CallTimeout
		}
		// The duration and the limit travel in the error, because the
		// error is what the proxy logs: a call that failed in 40ms and
		// one that ran out the clock read the same without them.
		note := fmt.Sprintf("after %s, timeout %s", c.Duration.Round(time.Millisecond), c.Timeout)
		if attempts > 1 {
			note += fmt.Sprintf(", %d attempts", attempts)
		}
		err = fmt.Errorf("%w (%s)", err, note)
		c.Err = err
	}
	if e.cfg.OnCall != nil {
		e.cfg.OnCall(c)
	}
	return res, err
}

// retryWait decides whether err earns another attempt and how long to wait
// first. It refuses when err is not a retryable provider answer, or when the
// wait would outlast the deadline.
func (e *Evaluator) retryWait(ctx context.Context, attempt int, err error) (time.Duration, bool) {
	var he *HTTPError
	if !errors.As(err, &he) || !he.Retryable() {
		return 0, false
	}
	// A provider's Retry-After wins, an explicit 0 included: it is the
	// provider saying "now", and a backoff in its place could push the
	// retry past a deadline the immediate attempt would fit.
	wait := he.RetryAfter
	if !he.HasRetryAfter {
		d := e.retryCap
		if shift := attempt - 1; shift < 16 {
			d = min(e.retryBase<<shift, e.retryCap)
		}
		// Full jitter in the upper half: retries from many connections
		// that failed together do not return together.
		wait = d/2 + rand.N(d/2+1)
	}
	if dl, ok := ctx.Deadline(); ok && time.Until(dl) <= wait {
		return 0, false
	}
	return wait, true
}

// observe reports an eligible statement's outcome through OnOutcome.
func (e *Evaluator) observe(status string, level RiskLevel, action Action) {
	if e.cfg.OnOutcome != nil {
		e.cfg.OnOutcome(Outcome{Rule: e.cfg.Rule, Status: status, RiskLevel: level, Action: action})
	}
}
