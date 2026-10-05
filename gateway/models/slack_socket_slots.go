package models

import (
	"time"

	"gorm.io/gorm"
)

// HoldSlackSocketSlot renews the slot holder has for the org, or claims a
// free or expired one among slots 0..slots-1. It reports whether holder ends
// up with a slot. Each statement is atomic, so two replicas never hold the
// same slot.
func HoldSlackSocketSlot(db *gorm.DB, orgID, holder string, slots int, ttl time.Duration) (bool, error) {
	expires := time.Now().UTC().Add(ttl)
	res := db.Exec(`
	UPDATE private.slack_socket_slots SET expires_at = ?
	WHERE org_id = ? AND holder = ? AND slot < ?`, expires, orgID, holder, slots)
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected > 0 {
		return true, nil
	}
	for slot := range slots {
		res := db.Exec(`
		INSERT INTO private.slack_socket_slots AS s (org_id, slot, holder, expires_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (org_id, slot) DO UPDATE SET holder = EXCLUDED.holder, expires_at = EXCLUDED.expires_at
		WHERE s.expires_at < NOW()`, orgID, slot, holder, expires)
		if res.Error != nil {
			return false, res.Error
		}
		if res.RowsAffected > 0 {
			return true, nil
		}
	}
	return false, nil
}

// ReleaseSlackSocketSlot frees every slot holder has for the org, so another
// replica can take it at once instead of after the lease runs out.
func ReleaseSlackSocketSlot(db *gorm.DB, orgID, holder string) error {
	return db.Exec(`DELETE FROM private.slack_socket_slots WHERE org_id = ? AND holder = ?`, orgID, holder).Error
}
