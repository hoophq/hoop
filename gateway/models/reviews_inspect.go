package models

import (
	"gorm.io/gorm"
)

// InspectReview is the slice of a review the hoop-inspect gate and the sandbox
// agent read back. It is deliberately narrow: these endpoints are reachable by
// a machine credential on the data path, and the full review row carries
// reviewer identities and the input blob, none of which the caller needs to
// decide whether its own statement may run.
type InspectReview struct {
	ID        string           `gorm:"column:id"`
	SessionID string           `gorm:"column:session_id"`
	Status    ReviewStatusType `gorm:"column:status"`
}

// ClaimInspectReview atomically consumes the caller's oldest approved review
// for this exact statement, moving it to EXECUTED, and returns it.
//
// It is the AUTHORIZATION step, not a read. Selecting and settling in one
// statement is what makes "single use" a property rather than a comment: two
// connections from one sandbox running the same statement against one approval
// means exactly one UPDATE returns a row, and the other is denied.
//
// It returns gorm.ErrRecordNotFound when nothing matches, which covers every
// denial case at once — no review, still PENDING, REJECTED, REVOKED, or an
// approval a previous execution already consumed.
//
// Scoping is applied from the credential the gateway authenticated, never from
// the request body:
//
//   - orgID and ownerID are the sandbox's own. Cross-sandbox reuse is
//     unreachable rather than defended against, because two sandboxes hold
//     different credentials with no access to each other's reviews.
//   - connectionID is doing real work. A sandbox may reach several
//     connections, so the credential alone does not distinguish them, and an
//     approval for appdb must not authorize the same SQL against payments-db.
//
// ORDER BY created_at LIMIT 1 is required, not cosmetic: statement_hash is not
// unique, the same statement legitimately accumulates many reviews over time,
// and more than one can be APPROVED at once. Without an ordering the choice is
// nondeterministic. FOR UPDATE SKIP LOCKED keeps two concurrent claims from
// blocking on each other — the loser finds no row and is denied, which is the
// correct answer for a single-use grant.
func ClaimInspectReview(db *gorm.DB, orgID, ownerID, connectionID, statementHash string) (*InspectReview, error) {
	var out InspectReview
	err := db.Raw(`
	UPDATE private.reviews SET status = ?
	 WHERE id = (
	   SELECT id FROM private.reviews
	    WHERE org_id         = ?
	      AND owner_id       = ?
	      AND connection_id  = ?
	      AND statement_hash = ?
	      AND status         = 'APPROVED'
	    ORDER BY created_at
	    LIMIT 1
	    FOR UPDATE SKIP LOCKED
	 )
	 RETURNING id, session_id, status`,
		ReviewStatusExecuted, orgID, ownerID, connectionID, statementHash).
		Scan(&out).Error
	if err != nil {
		return nil, err
	}
	if out.ID == "" {
		return nil, gorm.ErrRecordNotFound
	}
	return &out, nil
}

// GetPendingInspectReviewByMarker finds the review this sandbox already filed
// for this connection under this marker and that is still waiting on a human.
//
// It is the create path's dedupe, and it matches on the MARKER rather than the
// statement hash on purpose. The claim filters status = 'APPROVED', so a retry
// issued before a human has looked at the queue does not see its own PENDING
// review; without a separate dedupe key a polling agent would file one review
// per attempt.
//
// PENDING only. A rejected or revoked answer does not suppress a later request
// — the same statement can legitimately be asked about again, and each ask is
// answered on its own merits.
//
// Returns gorm.ErrRecordNotFound when there is none, which is the signal to
// create. A caller with an empty marker must not call this: no marker means
// every attempt is a new request, which is the safe default and the reason
// require_marker exists.
func GetPendingInspectReviewByMarker(db *gorm.DB, orgID, ownerID, connectionID, marker string) (*InspectReview, error) {
	var out InspectReview
	err := db.Raw(`
	SELECT id, session_id, status
	  FROM private.reviews
	 WHERE org_id        = ?
	   AND owner_id      = ?
	   AND connection_id = ?
	   AND request_marker = ?
	   AND status        = 'PENDING'
	 ORDER BY created_at DESC
	 LIMIT 1`, orgID, ownerID, connectionID, marker).
		Scan(&out).Error
	if err != nil {
		return nil, err
	}
	if out.ID == "" {
		return nil, gorm.ErrRecordNotFound
	}
	return &out, nil
}

// CloseInspectSession marks the session behind a claimed review finished.
//
// The session exists to anchor the review and give a reviewer something to
// open; no execution is ever streamed into it, because hoop-inspect forwards
// the statement itself and never reports the outcome back. Leaving it open
// would strand a row in the session list forever, so the claim closes it — and
// leaves exit_code NULL, which is the honest record of an outcome the gateway
// genuinely does not know.
//
// Guarded on ended_at IS NULL so a re-run cannot move the timestamp.
func CloseInspectSession(db *gorm.DB, orgID, sessionID string) error {
	return db.Exec(`
	UPDATE private.sessions
	   SET status = 'done', ended_at = NOW()
	 WHERE org_id = ? AND id = ? AND ended_at IS NULL`, orgID, sessionID).Error
}

// GetInspectReviewStatus reports where the caller's review for this statement
// stands, for the agent's poll loop. Read-only: it settles nothing, so polling
// can never consume an approval that the relay is the only thing allowed to
// consume.
//
// An APPROVED row wins over a newer row in any other status, because the
// question the agent is asking is "may I retry yet". Among rows in the same
// state the newest answers.
func GetInspectReviewStatus(db *gorm.DB, orgID, ownerID, connectionID, statementHash string) (*InspectReview, error) {
	var out InspectReview
	err := db.Raw(`
	SELECT id, session_id, status
	  FROM private.reviews
	 WHERE org_id         = ?
	   AND owner_id       = ?
	   AND connection_id  = ?
	   AND statement_hash = ?
	 ORDER BY (status = 'APPROVED') DESC, created_at DESC
	 LIMIT 1`, orgID, ownerID, connectionID, statementHash).
		Scan(&out).Error
	if err != nil {
		return nil, err
	}
	if out.ID == "" {
		return nil, gorm.ErrRecordNotFound
	}
	return &out, nil
}
