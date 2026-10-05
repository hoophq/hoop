package analyzer_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/inspect"
)

// scriptedProvider answers with errs in order, then with a high verdict.
type scriptedProvider struct {
	errs  []error
	calls atomic.Int64
}

func (s *scriptedProvider) Name() string { return "scripted" }

func (s *scriptedProvider) Classify(context.Context, string, string) (*analyzer.Result, error) {
	n := int(s.calls.Add(1)) - 1
	if n < len(s.errs) {
		return nil, s.errs[n]
	}
	return &analyzer.Result{RiskLevel: analyzer.RiskHigh, Title: "t"}, nil
}

func httpStatus(code int, retryAfter time.Duration) error {
	return &analyzer.HTTPError{Provider: "analyzer/stub", Code: code,
		Status: http.StatusText(code), RetryAfter: retryAfter}
}

// newRetrying builds a blocking evaluator that records its calls, with retry
// pacing short enough for a test.
func newRetrying(t *testing.T, p analyzer.Provider, retries int, timeout time.Duration) (*analyzer.Evaluator, *[]analyzer.Call) {
	t.Helper()
	var calls []analyzer.Call
	ev := mustNew(t, analyzer.Config{
		Provider:   p,
		Trigger:    deleteTrigger(),
		Actions:    analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionBlock},
		FailOpen:   true,
		Timeout:    timeout,
		MaxRetries: retries,
		OnCall:     func(c analyzer.Call) { calls = append(calls, c) },
	})
	analyzer.SetRetryPacing(ev, time.Millisecond, 2*time.Millisecond)
	return ev, &calls
}

// A 503 or 429 that clears is a verdict, not an outage: the statement is
// classified and the call reports both attempts.
func TestARetryableAnswerIsRetried(t *testing.T) {
	for _, code := range []int{http.StatusServiceUnavailable, http.StatusTooManyRequests} {
		p := &scriptedProvider{errs: []error{httpStatus(code, 0)}}
		ev, calls := newRetrying(t, p, 2, time.Second)

		v := ev.Evaluate(sqlStmt("DELETE FROM t", inspect.OpDelete, "t"))
		if !v.Denied {
			t.Errorf("%d then ok: not denied (err %v), want the high verdict to block", code, v.Err)
		}
		if len(*calls) != 1 || (*calls)[0].Attempts != 2 || (*calls)[0].Outcome != analyzer.CallOK {
			t.Errorf("%d then ok: calls = %+v, want one ok call of 2 attempts", code, *calls)
		}
	}
}

// A status that describes the request gets the same answer twice, so it is
// never retried. Neither is anything when max_retries is 0, the default.
func TestOnlyRetryableAnswersAreRetriedAndOnlyWhenAsked(t *testing.T) {
	for name, tc := range map[string]struct {
		err     error
		retries int
	}{
		"400":                 {httpStatus(http.StatusBadRequest, 0), 3},
		"403":                 {httpStatus(http.StatusForbidden, 0), 3},
		"transport error":     {errors.New("dial tcp: connection refused"), 3},
		"503, no max_retries": {httpStatus(http.StatusServiceUnavailable, 0), 0},
	} {
		p := &scriptedProvider{errs: []error{tc.err, tc.err, tc.err, tc.err}}
		ev, calls := newRetrying(t, p, tc.retries, time.Second)
		ev.Evaluate(sqlStmt("DELETE FROM t", inspect.OpDelete, "t"))
		if got := p.calls.Load(); got != 1 {
			t.Errorf("%s: provider called %d times, want 1", name, got)
		}
		if len(*calls) != 1 || (*calls)[0].Outcome != analyzer.CallError {
			t.Errorf("%s: calls = %+v, want one error", name, *calls)
		}
	}
}

// Retries stop at max_retries and the last answer is reported.
func TestRetriesStopAtTheLimit(t *testing.T) {
	e := httpStatus(http.StatusBadGateway, 0)
	p := &scriptedProvider{errs: []error{e, e, e, e, e}}
	ev, calls := newRetrying(t, p, 2, time.Second)

	v := ev.Evaluate(sqlStmt("DELETE FROM t", inspect.OpDelete, "t"))
	if got := p.calls.Load(); got != 3 {
		t.Errorf("provider called %d times, want 3 (1 + max_retries 2)", got)
	}
	if !errors.Is(v.Err, e) || !strings.Contains(v.Err.Error(), "3 attempts") {
		t.Errorf("Err = %v, want the 502 noting 3 attempts", v.Err)
	}
	if len(*calls) != 1 || (*calls)[0].Attempts != 3 {
		t.Errorf("calls = %+v, want one call of 3 attempts", *calls)
	}
}

// A Retry-After the deadline cannot cover ends the call at once with the
// provider's answer: waiting would hold the connection only to time out.
func TestARetryAfterPastTheDeadlineIsNotWaited(t *testing.T) {
	e := httpStatus(http.StatusTooManyRequests, time.Minute)
	p := &scriptedProvider{errs: []error{e, e}}
	ev, calls := newRetrying(t, p, 3, 200*time.Millisecond)

	start := time.Now()
	v := ev.Evaluate(sqlStmt("DELETE FROM t", inspect.OpDelete, "t"))
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("took %v: waited on a Retry-After past the deadline", elapsed)
	}
	if p.calls.Load() != 1 || !errors.Is(v.Err, e) {
		t.Errorf("calls %d, err %v: want one call reporting the 429", p.calls.Load(), v.Err)
	}
	if (*calls)[0].Outcome != analyzer.CallError {
		t.Errorf("outcome = %q, want error: the deadline never ran out", (*calls)[0].Outcome)
	}
}

// A call the deadline ends is a timeout, and the error says how long it ran
// and what the limit was: the proxy logs that error, and without the numbers
// a 40ms failure and an expired deadline read the same.
func TestATimeoutReportsItsDurationAndLimit(t *testing.T) {
	p := &stubProvider{level: analyzer.RiskHigh, delay: 2 * time.Second}
	ev, calls := newRetrying(t, p, 0, 50*time.Millisecond)

	v := ev.Evaluate(sqlStmt("DELETE FROM t", inspect.OpDelete, "t"))
	if !errors.Is(v.Err, context.DeadlineExceeded) {
		t.Fatalf("Err = %v, want deadline exceeded", v.Err)
	}
	if !strings.Contains(v.Err.Error(), "timeout 50ms") || !strings.Contains(v.Err.Error(), "(after ") {
		t.Errorf("Err = %q, want the duration and the 50ms limit", v.Err)
	}
	c := (*calls)[0]
	if c.Outcome != analyzer.CallTimeout || c.Duration < 50*time.Millisecond || c.Timeout != 50*time.Millisecond {
		t.Errorf("call = %+v, want a timeout of at least 50ms", c)
	}
}

// Every eligible statement reports what happened to it, cache hits
// included, because a hit is the statement nobody paid for.
func TestOutcomesCoverCacheHits(t *testing.T) {
	var got []analyzer.Outcome
	ev := mustNew(t, analyzer.Config{
		Rule:      "lane",
		Provider:  &stubProvider{level: analyzer.RiskHigh},
		Trigger:   deleteTrigger(),
		Actions:   analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionWarn},
		CacheSize: 8,
		CacheTTL:  time.Minute,
		OnOutcome: func(o analyzer.Outcome) { got = append(got, o) },
	})
	stmt := sqlStmt("DELETE FROM t", inspect.OpDelete, "t")
	ev.Evaluate(stmt)
	ev.Evaluate(stmt)
	ev.Evaluate(sqlStmt("SELECT 1", inspect.OpSelect))

	want := []analyzer.Outcome{
		{Rule: "lane", Status: analyzer.StatusOK, RiskLevel: analyzer.RiskHigh, Action: analyzer.ActionWarn},
		{Rule: "lane", Status: analyzer.StatusCached, RiskLevel: analyzer.RiskHigh, Action: analyzer.ActionWarn},
		{Rule: "lane", Status: analyzer.StatusSkipped},
	}
	if len(got) != len(want) {
		t.Fatalf("outcomes = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("outcome %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// Retry-After arrives as seconds or as an HTTP date; anything else is no
// hint at all, never a zero wait taken as an instruction.
func TestResponseErrorReadsRetryAfter(t *testing.T) {
	for _, tc := range []struct {
		header string
		min    time.Duration
		max    time.Duration
	}{
		{"7", 7 * time.Second, 7 * time.Second},
		{time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat), 28 * time.Second, 30 * time.Second},
		{"soon", 0, 0},
		{"", 0, 0},
	} {
		rec := httptest.NewRecorder()
		rec.Header().Set("Retry-After", tc.header)
		rec.WriteHeader(http.StatusTooManyRequests)
		err := analyzer.ResponseError("analyzer/x", rec.Result())

		var he *analyzer.HTTPError
		if !errors.As(err, &he) || !he.Retryable() {
			t.Fatalf("%q: err = %v, want a retryable HTTPError", tc.header, err)
		}
		if he.RetryAfter < tc.min || he.RetryAfter > tc.max {
			t.Errorf("%q: RetryAfter = %v, want in [%v, %v]", tc.header, he.RetryAfter, tc.min, tc.max)
		}
	}
}
