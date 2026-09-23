package analyzer

import (
	"context"
	"fmt"
	"time"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

// ReviewResult is what a review backend answered about one statement.
type ReviewResult struct {
	// Forward releases the statement, and is the ONLY field that may.
	//
	// A status cannot stand in for it. When two retries of one approved
	// statement race, both read the review as consumed afterwards, and
	// only the one that consumed it may release anything; no status tells
	// the two apart.
	Forward bool

	// ID identifies the review. It reaches the developer in the denial and
	// the audit record, and it is what they quote to an approver.
	ID string

	// Status is the backend's own word for where the review stands. It
	// picks the denial's wording (waiting, refused, spent) and nothing
	// else reads it, so a status this package has never heard of still
	// denies.
	Status string
}

// Review statuses, spelled as the control plane reports them. They are read
// only to word a denial: an unknown one falls through to a message that says
// the statement was not released, which is the safe reading of a vocabulary
// that grew a value this build predates.
const (
	reviewPending  = "PENDING"
	reviewRejected = "REJECTED"
	reviewRevoked  = "REVOKED"
	reviewExecuted = "EXECUTED"
)

// Reviewer is the review backend a hold talks to: the control plane, in the
// sidecar.
//
// Two calls, because waiting must never file. File is the first ask for a
// statement; Claim is every ask after it, about the review File named. A
// backend that files on a repeated ask would page approvers again whenever
// another connection spent the approval mid-wait.
type Reviewer interface {
	// File files the statement for approval, or answers from the review
	// already filed for these exact bytes. It receives the RAW statement
	// text, never the model input. See hold.
	File(ctx context.Context, statement string) (ReviewResult, error)

	// Claim answers about one review by id, spending it when it is
	// approved. It never files.
	Claim(ctx context.Context, reviewID string) (ReviewResult, error)
}

// How long a held statement waits on its connection, and how often it asks.
//
// Constants, not configuration, and the same on every protocol: a client that
// gives up sooner disconnects, which ends the wait without spending the
// approval, so the caller's own deadline is the budget in practice. The lane's
// idle_timeout_sec ends it too, since a waiting client sends nothing.
// One held statement costs ReviewWait/reviewPoll calls, 360 at these values.
// Exported so the daemon can compare it with a lane's idle timeout.
const (
	ReviewWait = 30 * time.Minute
	reviewPoll = 5 * time.Second
)

// hold resolves an ActionRequireReview verdict: it files the statement for
// human approval, waits on the connection while the review is pending, and
// forwards only what came back released.
//
// It is the one place in this package that talks to anything but the model
// provider, and it fails closed in every direction. FailOpen is not consulted
// here at all: an operator who allowed a classification outage to pass traffic
// was talking about a model that could not answer, not about a human gate
// that could not be reached.
//
// The RAW statement goes out (see reviewText), never content.Text. The backend
// matches an approval against the exact bytes a retry sends, so a redacted or truncated
// rendering would either match nothing or, worse, approve something nobody
// read.
//
// ctx is the connection's. It ends the wait when the client or the upstream
// goes away, so an approval is never spent on a statement that cannot run.
func (e *Evaluator) hold(ctx context.Context, stmt inspect.Statement, notes map[string]string) policy.Verdict {
	if e.cfg.Review == nil {
		// An observed lane is built without one on purpose, and its
		// denial is turned back into an allow by policy.Observe. Any
		// other lane reaching here has no way to file, and a hold that
		// cannot file has to deny. It never waits: a rehearsal must not
		// stall on a human.
		return e.denyHold(notes, "", "no review backend is configured")
	}
	if err := ctx.Err(); err != nil {
		// Gone before anything was filed: paging a human for a statement
		// that can no longer run is noise.
		return e.denyEnded(ctx, notes, "", "the connection ended before the review was filed")
	}

	text, err := reviewText(stmt)
	if err != nil {
		return e.denyHold(notes, "", err.Error())
	}
	res, err := e.ask(ctx, func(c context.Context) (ReviewResult, error) {
		return e.cfg.Review.File(c, text)
	})
	if err != nil {
		e.errs.Add(1)
		v := e.denyHold(notes, res.ID, "the review could not be filed")
		v.Err = err
		return v
	}
	if res.ID != "" {
		notes[MetadataReviewID] = res.ID
	}
	if res.Forward {
		return e.release(ctx, notes, res.ID)
	}
	if res.Status != reviewPending || res.ID == "" {
		return e.denyHold(notes, res.ID, reviewReason(res.Status))
	}
	return e.wait(ctx, res.ID, notes)
}

// reviewText renders the bytes the backend files and matches an approval
// against.
//
// A truncated body refuses on every lane that carries one (http, grpc,
// spanner): an approval would bind to bytes the reviewer never read, and
// release any message sharing that prefix. A grpc statement's Text already
// holds the rendered message. An http statement's Text is only the method and
// target, so filing it alone would let one approval release any body sent to
// that path; the body goes with it. A query value the codec redacted still
// matches across requests; the codec never hands over the raw one.
func reviewText(stmt inspect.Statement) (string, error) {
	if stmt.HTTP == nil {
		return stmt.Text, nil
	}
	if stmt.HTTP.BodyTruncated {
		budget := "http.max_body_bytes"
		if stmt.Protocol != inspect.HTTP {
			budget = "grpc.max_payload_bytes"
		}
		return "", fmt.Errorf("the request body is larger than %s, "+
			"so a reviewer could not read all of it", budget)
	}
	if stmt.Protocol != inspect.HTTP || stmt.HTTP.Body == "" {
		return stmt.Text, nil
	}
	return stmt.Text + "\n\n" + stmt.HTTP.Body, nil
}

// wait asks about one pending review until it settles, the budget runs out or
// the connection ends.
//
// It asks by id and never files. A statement whose approval another
// connection spent reads EXECUTED here and stops, rather than filing a fresh
// review and paging the approvers again from inside a wait.
//
// The budget bounds when a poll STARTS, not when one ends: a claim in flight
// at the deadline may already have spent the approval, so its answer is
// honored.
func (e *Evaluator) wait(ctx context.Context, reviewID string, notes map[string]string) policy.Verdict {
	deadline := time.Now().Add(e.reviewWait)
	timer := time.NewTimer(e.reviewPoll)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
		case <-timer.C:
		}
		// Checked after the select too: both cases can be ready at once,
		// and a claim started for a gone connection can spend the approval.
		if ctx.Err() != nil {
			return e.denyEnded(ctx, notes, reviewID, "the connection ended while waiting for approval")
		}

		res, err := e.ask(ctx, func(c context.Context) (ReviewResult, error) {
			return e.cfg.Review.Claim(c, reviewID)
		})
		if err != nil {
			e.errs.Add(1)
			v := e.denyHold(notes, reviewID, "the review could not be checked")
			v.Err = err
			return v
		}
		if res.Forward {
			return e.release(ctx, notes, reviewID)
		}
		if res.Status != reviewPending {
			return e.denyHold(notes, reviewID, reviewReason(res.Status))
		}

		left := time.Until(deadline)
		if left <= 0 {
			return e.denyHold(notes, reviewID, fmt.Sprintf(
				"still waiting for approval after %s; run the statement again once it is approved",
				e.reviewWait))
		}
		timer.Reset(min(e.reviewPoll, left))
	}
}

// ask makes one call to the review backend, bounded by the analyzer timeout.
//
// The call is detached from the connection's cancellation on purpose. Once a
// request is out, the plane may spend the approval whether or not anybody
// reads the answer, and an answer that is read can at least be recorded. The
// caller checks the connection before starting a call, and release checks it
// again after a claim.
func (e *Evaluator) ask(ctx context.Context, call func(context.Context) (ReviewResult, error)) (ReviewResult, error) {
	callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), e.cfg.Timeout)
	defer cancel()
	return call(callCtx)
}

// release forwards a statement whose approval the backend just spent, unless
// the connection ended while the claim was in flight. The approval is gone
// either way; the record says so, and names the review that was spent.
func (e *Evaluator) release(ctx context.Context, notes map[string]string, reviewID string) policy.Verdict {
	if ctx.Err() != nil {
		return e.denyEnded(ctx, notes, reviewID,
			"the approval was used, but the connection ended before the statement could run")
	}
	// The backend consumed an approved review for these exact bytes, which
	// it does once. The statement travels, and the audit record carries the
	// review that released it.
	return policy.Verdict{Annotations: notes}
}

// denyEnded refuses a statement whose connection ended during the hold. The
// client rarely reads this; the audit record does, so the cause travels in
// the message and on Err.
func (e *Evaluator) denyEnded(ctx context.Context, notes map[string]string, reviewID, reason string) policy.Verdict {
	cause := context.Cause(ctx)
	v := e.denyHold(notes, reviewID, reason+": "+cause.Error())
	v.Err = cause
	return v
}

// denyHold builds the refusal a held statement produces.
//
// reason is why the statement is not moving. It is rendered after the
// operator's message rather than instead of it, because the two answer
// different questions: the operator says what to do about it, the reason says
// what the statement is waiting on.
func (e *Evaluator) denyHold(notes map[string]string, reviewID, reason string) policy.Verdict {
	e.denied.Add(1)
	v := policy.Deny(e.cfg.Rule, holdMessage(e.cfg.Message, reviewID, reason))
	v.Source = policy.SourceAnalyzer
	v.Annotations = notes
	return v
}

// holdMessage renders what the developer reads in their client.
//
// It reaches the wire in the protocol's error frame, so it carries an id and
// a state and nothing the statement contained. The id is the whole point of
// the line: without it the developer cannot tell an approver which request to
// look at.
func holdMessage(operator, reviewID, reason string) string {
	msg := operator
	if msg == "" {
		msg = "statement held for human approval"
	}
	if reviewID == "" {
		return msg + ": " + reason
	}
	return fmt.Sprintf("%s: %s (review %s)", msg, reason, reviewID)
}

// reviewReason turns a review's status into the clause a developer reads.
//
// The distinction that matters to them is whether waiting will help. Pending
// says retry later; rejected and revoked say stop, because the backend keeps
// a refusal and files nothing new for the same statement.
func reviewReason(status string) string {
	switch status {
	case reviewPending:
		return "waiting for approval"
	case reviewRejected:
		return "the review was rejected"
	case reviewRevoked:
		return "the review was revoked"
	case reviewExecuted:
		// Reached by the loser of a claim race: another connection
		// consumed this approval a moment ago. An approval releases one
		// statement once, so this one needs a new review.
		return "the approval was already used"
	}
	return "the statement was not released"
}
