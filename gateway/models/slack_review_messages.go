package models

import (
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
)

// SlackReviewMessage is one review message posted to one Slack channel.
// Blocks is the block set as posted, JSON-encoded by gateway/slack.
type SlackReviewMessage struct {
	ReviewID  string          `gorm:"column:review_id"`
	OrgID     string          `gorm:"column:org_id"`
	ChannelID string          `gorm:"column:channel_id"`
	Timestamp string          `gorm:"column:ts"`
	EventKind string          `gorm:"column:event_kind"`
	Blocks    json.RawMessage `gorm:"column:blocks"`
	SentAt    time.Time       `gorm:"column:sent_at"`
}

// SlackReviewSettlement is the terminal rewrite a review got. Rewritable
// marks an approval, whose messages a revoke rewrites once more.
type SlackReviewSettlement struct {
	ReviewID   string          `gorm:"column:review_id"`
	OrgID      string          `gorm:"column:org_id"`
	Request    json.RawMessage `gorm:"column:request"`
	Rewritable bool            `gorm:"column:rewritable"`
	SettledAt  time.Time       `gorm:"column:settled_at"`
}

// InsertSlackReviewMessage records one posted message. It also drops rows
// older than retention, which keeps the table bounded without a job.
func InsertSlackReviewMessage(db *gorm.DB, m SlackReviewMessage, retention time.Duration) error {
	cutoff := time.Now().UTC().Add(-retention)
	if err := db.Exec(`DELETE FROM private.slack_review_messages WHERE sent_at < ?`, cutoff).Error; err != nil {
		return err
	}
	if err := db.Exec(`DELETE FROM private.slack_review_settlements WHERE settled_at < ?`, cutoff).Error; err != nil {
		return err
	}
	return db.Exec(`
	INSERT INTO private.slack_review_messages (review_id, org_id, channel_id, ts, event_kind, blocks, sent_at)
	VALUES (?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT (review_id, channel_id, ts) DO NOTHING`,
		m.ReviewID, m.OrgID, m.ChannelID, m.Timestamp, m.EventKind, string(m.Blocks), m.SentAt).Error
}

// ListSlackReviewMessages returns every message posted for a review, oldest
// first.
func ListSlackReviewMessages(db *gorm.DB, reviewID string) ([]SlackReviewMessage, error) {
	var out []SlackReviewMessage
	err := db.Raw(`
	SELECT review_id, org_id, channel_id, ts, event_kind, blocks, sent_at
	FROM private.slack_review_messages
	WHERE review_id = ?
	ORDER BY sent_at, channel_id, ts`, reviewID).Scan(&out).Error
	return out, err
}

// GetSlackReviewSettlement returns the review's settlement, or
// gorm.ErrRecordNotFound when it has not settled.
func GetSlackReviewSettlement(db *gorm.DB, reviewID string) (*SlackReviewSettlement, error) {
	var out SlackReviewSettlement
	res := db.Raw(`
	SELECT review_id, org_id, request, rewritable, settled_at
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

// UpsertSlackReviewSettlement records the review's latest terminal rewrite.
func UpsertSlackReviewSettlement(db *gorm.DB, s SlackReviewSettlement) error {
	if s.ReviewID == "" {
		return errors.New("a slack review settlement needs a review id")
	}
	return db.Exec(`
	INSERT INTO private.slack_review_settlements (review_id, org_id, request, rewritable, settled_at)
	VALUES (?, ?, ?, ?, NOW())
	ON CONFLICT (review_id) DO UPDATE SET
		request = EXCLUDED.request, rewritable = EXCLUDED.rewritable, settled_at = EXCLUDED.settled_at`,
		s.ReviewID, s.OrgID, string(s.Request), s.Rewritable).Error
}
