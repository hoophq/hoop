package services

import (
	"errors"
	"fmt"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
	"gorm.io/gorm"
)

// ErrNoApprovedReview means nothing authorized this statement.
//
// One error for every denial, deliberately: no review was ever filed, one is
// still PENDING, one was REJECTED or REVOKED, or an approval a previous
// execution already consumed. The caller is a machine on the data path and the
// answer it needs is the same in every case — not yet — while distinguishing
// them would tell an unauthenticated-for-this-statement caller what exists.
var ErrNoApprovedReview = errors.New("no approved review for this statement")

// ClaimInspectReview consumes the caller's approval for one exact statement
// and settles the session that anchored it.
//
// This is the AUTHORIZATION step of the hoop-inspect gate, and the domain rule
// it owns is that consuming an approval is a WORKFLOW rather than a write:
// the approval moves APPROVED -> EXECUTED, and the session that existed only
// to give a reviewer something to open is finished in the same breath. Neither
// half is meaningful alone, which is why they are not left to a caller to
// sequence correctly.
//
// The single-use guarantee is NOT enforced here. It lives in the models layer
// as one atomic UPDATE ... FOR UPDATE SKIP LOCKED, because a select-then-write
// pair in this function would let two concurrent claims both observe the same
// APPROVED row. Do not lift that SQL into this layer to make it "read better":
// the atomicity is the feature.
//
// Scoping comes from the credential the gateway already authenticated. Callers
// pass orgID and ownerID from the authenticated context and connectionID from
// an access-control-aware lookup — never from a request body.
//
// Returns ErrNoApprovedReview when nothing matches.
func ClaimInspectReview(orgID, ownerID, connectionID, statementHash string) (*models.InspectReview, error) {
	rev, err := models.ClaimInspectReview(models.DB, orgID, ownerID, connectionID, statementHash)
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return nil, ErrNoApprovedReview
	case err != nil:
		return nil, fmt.Errorf("failed claiming inspect review: %w", err)
	}

	// Best effort, and it must stay that way. The approval is already spent
	// by the statement above; failing now would refuse a statement that IS
	// authorized and cost a human a second approval to recover. A session
	// left open is a cosmetic problem, so it is logged and swallowed.
	if err := models.CloseInspectSession(models.DB, orgID, rev.SessionID); err != nil {
		log.With("org", orgID, "sid", rev.SessionID).
			Warnf("failed closing session for claimed inspect review: %v", err)
	}
	return rev, nil
}

// GetInspectReviewStatus reports where the caller's review for one statement
// stands, for a waiting agent's poll loop.
//
// Read-only, and that is a security property rather than an optimization:
// polling must never consume an approval, which only ClaimInspectReview may
// do. Keeping the two on separate paths means a poll cannot be turned into an
// execution grant by an agent that calls it in a loop.
//
// Returns ErrNoApprovedReview when the caller has no review for this
// statement at all.
func GetInspectReviewStatus(orgID, ownerID, connectionID, statementHash string) (*models.InspectReview, error) {
	rev, err := models.GetInspectReviewStatus(models.DB, orgID, ownerID, connectionID, statementHash)
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return nil, ErrNoApprovedReview
	case err != nil:
		return nil, fmt.Errorf("failed fetching inspect review status: %w", err)
	}
	return rev, nil
}

// FindPendingInspectReviewByMarker reports the review this caller already
// filed for this connection under this marker and that is still waiting on a
// human, so a retry joins it instead of filing a duplicate.
//
// Marker-keyed rather than hash-keyed on purpose: only the marker carries the
// caller's intent. An agent whose task 3 and task 9 run byte-identical SQL is
// making two requests, and each still needs its own human.
//
// A caller with an empty marker MUST NOT call this — no marker means every
// attempt is a new request, which is the safe default — so an empty marker is
// refused rather than matched against the rows that have none.
//
// Returns ErrNoApprovedReview when there is no such review, which is the
// signal to create one.
func FindPendingInspectReviewByMarker(orgID, ownerID, connectionID, marker string) (*models.InspectReview, error) {
	if marker == "" {
		return nil, ErrNoApprovedReview
	}
	rev, err := models.GetPendingInspectReviewByMarker(models.DB, orgID, ownerID, connectionID, marker)
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return nil, ErrNoApprovedReview
	case err != nil:
		return nil, fmt.Errorf("failed looking up pending inspect review: %w", err)
	}
	return rev, nil
}
