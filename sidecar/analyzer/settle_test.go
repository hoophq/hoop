package analyzer_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/policy"
)

// settlingReviewer is a backend that reserves approvals
// (spend_approval_on: accepted) and records every settle.
type settlingReviewer struct {
	recordingReviewer

	mu        sync.Mutex
	settled   []string
	settleErr error
}

func (r *settlingReviewer) Settle(_ context.Context, reviewID string, accepted bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.settled = append(r.settled, reviewID+"="+map[bool]string{true: "accepted", false: "refused"}[accepted])
	return r.settleErr
}

func (r *settlingReviewer) settles() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.settled)
}

func settlingEvaluator(t *testing.T, rev *settlingReviewer, edit func(*analyzer.Config)) *analyzer.Evaluator {
	t.Helper()
	return holdingEvaluator(t, nil, func(c *analyzer.Config) {
		c.Review = rev
		if edit != nil {
			edit(c)
		}
	})
}

var reserved = analyzer.ReviewResult{Forward: true, ID: "9f97", Status: "PROCESSING"}

// A reservation releases the statement and tells the gate which approval
// the upstream's answer settles.
func TestAReservedApprovalReleasesAndNamesTheReservation(t *testing.T) {
	rev := &settlingReviewer{recordingReviewer: recordingReviewer{res: reserved}}
	v := settlingEvaluator(t, rev, nil).Evaluate(deleteStatement())

	if v.Denied {
		t.Fatalf("a reserved approval did not forward: %q", v.Message)
	}
	if v.Review == nil || !v.Review.Reserved || v.Review.ID != "9f97" {
		t.Fatalf("the verdict carries %+v, want the reservation of 9f97", v.Review)
	}
	if got := rev.settles(); len(got) != 0 {
		t.Errorf("the analyzer settled %v; the gate settles once the upstream answers", got)
	}
}

// A spent approval has nothing left to settle.
func TestASpentApprovalNamesNoReservation(t *testing.T) {
	rev := &settlingReviewer{recordingReviewer: recordingReviewer{res: analyzer.ReviewResult{
		Forward: true, ID: "9f97", Status: "EXECUTED",
	}}}
	v := settlingEvaluator(t, rev, nil).Evaluate(deleteStatement())
	if v.Denied || v.Review != nil {
		t.Errorf("denied=%v review=%+v, want an allow with no reservation", v.Denied, v.Review)
	}
}

// Another request is using the approval. A refusal upstream gives it back,
// so a hold waits for it rather than giving up.
func TestAHoldWaitsOnAnApprovalInUse(t *testing.T) {
	rev := &settlingReviewer{recordingReviewer: recordingReviewer{
		res:    analyzer.ReviewResult{ID: "9f97", Status: "PROCESSING"},
		claims: []analyzer.ReviewResult{{ID: "9f97", Status: "PROCESSING"}, reserved},
	}}
	v := settlingEvaluator(t, rev, nil).Evaluate(deleteStatement())

	if v.Denied {
		t.Fatalf("the approval given back was not taken: %q", v.Message)
	}
	if got := len(rev.claimedIDs()); got != 2 {
		t.Errorf("claimed %d times, want 2", got)
	}
	if v.Review == nil || !v.Review.Reserved {
		t.Errorf("the verdict carries %+v, want a reservation", v.Review)
	}
}

func TestAHoldStopsWhenTheOtherRequestSpendsTheApproval(t *testing.T) {
	rev := &settlingReviewer{recordingReviewer: recordingReviewer{
		res:    analyzer.ReviewResult{ID: "9f97", Status: "PROCESSING"},
		claims: []analyzer.ReviewResult{{ID: "9f97", Status: "EXECUTED"}},
	}}
	v := settlingEvaluator(t, rev, nil).Evaluate(deleteStatement())
	if !v.Denied || !strings.Contains(v.Message, "already used") {
		t.Errorf("denied=%v message=%q, want a denial saying the approval was used", v.Denied, v.Message)
	}
}

// Return mode never waits: the agent checks the review and resends.
func TestReturnModeDeniesAnApprovalInUse(t *testing.T) {
	rev := &settlingReviewer{recordingReviewer: recordingReviewer{
		res: analyzer.ReviewResult{ID: "9f97", Status: "PROCESSING"},
	}}
	v := settlingEvaluator(t, rev, returnMode).Evaluate(deleteStatement())

	if !v.Denied || !strings.Contains(v.Message, "another request is using this approval") ||
		!strings.Contains(v.Message, "9f97") {
		t.Errorf("denied=%v message=%q, want a denial naming the approval in use", v.Denied, v.Message)
	}
	if got := len(rev.claimedIDs()); got != 0 {
		t.Errorf("return mode claimed %d times", got)
	}
}

// Nothing ran, so the reserved approval goes back.
func TestAReservationWhoseConnectionEndedIsGivenBack(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	rev := &settlingReviewer{recordingReviewer: recordingReviewer{
		res:    analyzer.ReviewResult{ID: "9f97", Status: "PENDING"},
		claims: []analyzer.ReviewResult{reserved},
	}}
	rev.onClaim = func() { cancel(errors.New("the client disconnected")) }
	v := settlingEvaluator(t, rev, nil).EvaluateWith(deleteStatement(), &policy.EvalContext{ConnCtx: ctx})

	if !v.Denied || !strings.Contains(v.Message, "given back") {
		t.Errorf("denied=%v message=%q, want a denial saying the approval was given back", v.Denied, v.Message)
	}
	if got := rev.settles(); !slices.Equal(got, []string{"9f97=refused"}) {
		t.Errorf("settles = %v, want [9f97=refused]", got)
	}
}

// A give-back the plane did not take is spent when the lease ends: the
// record must not claim the approval came back.
func TestAFailedGiveBackSaysTheApprovalWasUsed(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	rev := &settlingReviewer{
		recordingReviewer: recordingReviewer{
			res:    analyzer.ReviewResult{ID: "9f97", Status: "PENDING"},
			claims: []analyzer.ReviewResult{reserved},
		},
		settleErr: errors.New("the control plane is unreachable"),
	}
	rev.onClaim = func() { cancel(errors.New("the client disconnected")) }
	v := settlingEvaluator(t, rev, nil).EvaluateWith(deleteStatement(), &policy.EvalContext{ConnCtx: ctx})

	if !v.Denied || !strings.Contains(v.Message, "approval was used") {
		t.Errorf("denied=%v message=%q, want a denial saying the approval was used", v.Denied, v.Message)
	}
}
