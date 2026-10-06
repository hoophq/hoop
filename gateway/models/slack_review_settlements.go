package models

import (
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
)

// SlackReviewSettlement is the terminal rewrite a review got. Rewritable
// marks an approval, whose messages a revoke or an expiry rewrites once more.
// Deadline is that approval's deadline; the row outlives it by the retention.
type SlackReviewSettlement struct {
	ReviewID   string          `gorm:"column:review_id"`
	OrgID      string          `gorm:"column:org_id"`
	Request    json.RawMessage `gorm:"column:request"`
	Rewritable bool            `gorm:"column:rewritable"`
	SettledAt  time.Time       `gorm:"column:settled_at"`
	Deadline   *time.Time      `gorm:"column:deadline"`
}

// GetSlackReviewSettlement returns the review's settlement, or
// gorm.ErrRecordNotFound when it has not settled.
func GetSlackReviewSettlement(db *gorm.DB, reviewID string) (*SlackReviewSettlement, error) {
	var out SlackReviewSettlement
	res := db.Raw(`
	SELECT review_id, org_id, request, rewritable, settled_at, deadline
	FROM private.slack_review_settlements
	WHERE review_id = ?`, reviewID).Scan(&out)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return &out, nil
}

// UpsertSlackReviewSettlement records the review's latest terminal rewrite,
// and reports whether it did. A revoke or an expiry is final: a later write,
// such as an approval whose rewrite ran late, leaves it in place and reports
// false.
func UpsertSlackReviewSettlement(db *gorm.DB, s SlackReviewSettlement) (bool, error) {
	if s.ReviewID == "" {
		return false, errors.New("a slack review settlement needs a review id")
	}
	res := db.Exec(`
	INSERT INTO private.slack_review_settlements AS st (review_id, org_id, request, rewritable, settled_at, deadline)
	VALUES (?, ?, ?, ?, NOW(), ?)
	ON CONFLICT (review_id) DO UPDATE SET
		request = EXCLUDED.request, rewritable = EXCLUDED.rewritable, settled_at = EXCLUDED.settled_at,
		deadline = EXCLUDED.deadline
	WHERE NOT COALESCE((st.request->>'IsRevoked')::boolean, false)
		AND NOT COALESCE((st.request->>'IsExpired')::boolean, false)`,
		s.ReviewID, s.OrgID, string(s.Request), s.Rewritable, s.Deadline)
	return res.RowsAffected > 0, res.Error
}

// deleteSlackReviewSettlementsBefore drops settlements whose settlement and
// deadline are both older than cutoff.
// InsertSlackReviewMessage calls it, so the table stays bounded without a job.
func deleteSlackReviewSettlementsBefore(db *gorm.DB, cutoff time.Time) error {
	return db.Exec(`DELETE FROM private.slack_review_settlements
	WHERE settled_at < ? AND (deadline IS NULL OR deadline < ?)`, cutoff, cutoff).Error
}
