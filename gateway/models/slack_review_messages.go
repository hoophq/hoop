package models

import (
	"encoding/json"
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

// InsertSlackReviewMessage records one posted message. It also drops rows
// older than retention, which keeps the table bounded without a job.
func InsertSlackReviewMessage(db *gorm.DB, m SlackReviewMessage, retention time.Duration) error {
	cutoff := time.Now().UTC().Add(-retention)
	if err := db.Exec(`DELETE FROM private.slack_review_messages WHERE sent_at < ?`, cutoff).Error; err != nil {
		return err
	}
	if err := deleteSlackReviewSettlementsBefore(db, cutoff); err != nil {
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
