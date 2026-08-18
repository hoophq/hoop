package services

import (
	"context"

	"github.com/hoophq/hoop/gateway/models"
	"gorm.io/gorm"
)

// PersistRDPGuardrailViolation appends one report and its detections in a
// transaction. The session row is locked and reportID is checked inside its
// guardrails_info JSON array, making an agent retry idempotent without a
// separate side table.
func PersistRDPGuardrailViolation(
	ctx context.Context,
	db *gorm.DB,
	orgID, sessionID, reportID string,
	info []byte,
	detections []models.RDPEntityDetection,
) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var lockedSessionID string
		lock := tx.Raw(
			`SELECT id FROM private.sessions WHERE org_id = ? AND id = ? FOR UPDATE`,
			orgID,
			sessionID,
		).Scan(&lockedSessionID)
		if lock.Error != nil {
			return lock.Error
		}
		if lock.RowsAffected == 0 {
			return models.ErrNotFound
		}

		var duplicate bool
		if err := tx.Raw(
			`SELECT EXISTS (
				SELECT 1
				FROM jsonb_array_elements(COALESCE(guardrails_info, '[]'::jsonb)) AS item
				WHERE item->>'report_id' = ?
			)
			FROM private.sessions
			WHERE org_id = ? AND id = ?`,
			reportID,
			orgID,
			sessionID,
		).Scan(&duplicate).Error; err != nil {
			return err
		}
		if duplicate {
			return nil
		}

		update := tx.Table("private.sessions").
			Where("org_id = ? AND id = ?", orgID, sessionID).
			Update(
				"guardrails_info",
				gorm.Expr("COALESCE(guardrails_info, '[]'::jsonb) || ?::jsonb", info),
			)
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected == 0 {
			return models.ErrNotFound
		}
		if len(detections) == 0 {
			return nil
		}
		return tx.Table("private.rdp_entity_detections").
			Omit("id").
			CreateInBatches(detections, 100).Error
	})
}
