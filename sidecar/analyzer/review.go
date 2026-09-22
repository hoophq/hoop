package analyzer

import (
	"context"
	"fmt"

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

// hold resolves an ActionRequireReview verdict: it files the statement for
// human approval and forwards only what came back released.
//
// It is the one place in this package that talks to anything but the model
// provider, and it fails closed in every direction. FailOpen is not consulted
// here at all: an operator who allowed a classification outage to pass traffic
// was talking about a model that could not answer, not about a human gate
// that could not be reached.
//
// The RAW statement text goes out, never content.Text. The backend matches an
// approval against the exact bytes a retry sends, so a redacted or truncated
// rendering would either match nothing or, worse, approve something nobody
// read.
func (e *Evaluator) hold(stmt inspect.Statement, notes map[string]string) policy.Verdict {
	if e.cfg.Review == nil {
		// An observed lane is built without one on purpose, and its
		// denial is turned back into an allow by policy.Observe. Any
		// other lane reaching here has no way to file, and a hold that
		// cannot file has to deny.
		return e.denyHold(notes, "", "no review backend is configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), e.cfg.Timeout)
	defer cancel()

	res, err := e.cfg.Review(ctx, stmt.Text)
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
		// The backend consumed an approved review for these exact bytes,
		// which it does once. The statement travels, and the audit record
		// carries the review that released it.
		return policy.Verdict{Annotations: notes}
	}
	return e.denyHold(notes, res.ID, reviewReason(res.Status))
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
