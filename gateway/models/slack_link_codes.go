package models

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// ErrSlackIDInUse reports a Slack ID that another user of the org has.
var ErrSlackIDInUse = errors.New("slack id is linked to another user")

// SetSlackLinkCode stores the hash of the user's one-time link code, in place
// of the one the user had, and drops the org's expired codes.
func SetSlackLinkCode(db *gorm.DB, orgID, userID, codeHash string, expiresAt time.Time) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`DELETE FROM private.slack_link_codes WHERE org_id = ? AND expires_at <= NOW()`,
			orgID).Error; err != nil {
			return err
		}
		return tx.Exec(`
			INSERT INTO private.slack_link_codes (org_id, user_id, code_hash, expires_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT (org_id, user_id) DO UPDATE
			SET code_hash = EXCLUDED.code_hash, expires_at = EXCLUDED.expires_at, created_at = NOW()`,
			orgID, userID, codeHash, expiresAt).Error
	})
}

// RedeemSlackLinkCode links slackID to the user who holds the live code with
// this hash, and returns that user. The code works once.
//
//   - No live code has the hash: ErrNotFound.
//   - Another user of the org has slackID: ErrSlackIDInUse, and the code
//     stays live.
func RedeemSlackLinkCode(db *gorm.DB, orgID, codeHash, slackID string) (*User, error) {
	var user User
	err := db.Transaction(func(tx *gorm.DB) error {
		var userID string
		res := tx.Raw(`
			DELETE FROM private.slack_link_codes
			WHERE org_id = ? AND code_hash = ? AND expires_at > NOW()
			RETURNING user_id`, orgID, codeHash).Scan(&userID)
		if res.Error != nil {
			return res.Error
		}
		if userID == "" {
			return ErrNotFound
		}
		var others int64
		if err := tx.Raw(`SELECT count(*) FROM private.users WHERE org_id = ? AND slack_id = ? AND id <> ?`,
			orgID, slackID, userID).Scan(&others).Error; err != nil {
			return err
		}
		if others > 0 {
			return ErrSlackIDInUse
		}
		if err := tx.Exec(`UPDATE private.users SET slack_id = ? WHERE org_id = ? AND id = ?`,
			slackID, orgID, userID).Error; err != nil {
			return err
		}
		return tx.Where("org_id = ? AND id = ?", orgID, userID).First(&user).Error
	})
	if err != nil {
		return nil, err
	}
	return &user, nil
}
