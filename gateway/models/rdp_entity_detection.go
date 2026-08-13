package models

import (
	"context"

	"gorm.io/gorm"
)

// RDPEntityDetection represents a single PII entity detected in an RDP session frame.
// Coordinates (X, Y, Width, Height) are in screen-space pixels, matching the canvas
// coordinate system used by the RDP session replay player.
type RDPEntityDetection struct {
	ID         string  `gorm:"column:id;primaryKey" json:"id"`
	SessionID  string  `gorm:"column:session_id" json:"session_id"`
	FrameIndex int     `gorm:"column:frame_index" json:"frame_index"`
	Timestamp  float64 `gorm:"column:timestamp" json:"timestamp"`
	EntityType string  `gorm:"column:entity_type" json:"entity_type"`
	Score      float64 `gorm:"column:score" json:"score"`
	X          int     `gorm:"column:x" json:"x"`
	Y          int     `gorm:"column:y" json:"y"`
	Width      int     `gorm:"column:width" json:"width"`
	Height     int     `gorm:"column:height" json:"height"`
}

// BulkInsertRDPEntityDetections inserts a batch of entity detections for an RDP session.
// Uses CreateInBatches to avoid oversized INSERT statements.
func BulkInsertRDPEntityDetections(detections []RDPEntityDetection) error {
	if len(detections) == 0 {
		return nil
	}
	return DB.Table("private.rdp_entity_detections").
		Omit("id").
		CreateInBatches(detections, 100).Error
}

// PersistRDPGuardrailViolation appends one report and its detections in a
// transaction. The session row is locked and report_id is checked inside its
// guardrails_info JSON array, making an agent retry idempotent without a
// separate side table.
func PersistRDPGuardrailViolation(
	ctx context.Context,
	orgID, sessionID, reportID string,
	info []byte,
	detections []RDPEntityDetection,
) error {
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
			return ErrNotFound
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
			return ErrNotFound
		}
		if len(detections) == 0 {
			return nil
		}
		return tx.Table("private.rdp_entity_detections").
			Omit("id").
			CreateInBatches(detections, 100).Error
	})
}

// GetRDPEntityDetections returns all entity detections for a session, ordered by frame index.
func GetRDPEntityDetections(sessionID string) ([]RDPEntityDetection, error) {
	var detections []RDPEntityDetection
	err := DB.Table("private.rdp_entity_detections").
		Where("session_id = ?", sessionID).
		Order("frame_index ASC, id ASC").
		Find(&detections).Error
	return detections, err
}

// DeleteRDPEntityDetections removes all entity detections for a session.
// Useful for re-analysis scenarios.
func DeleteRDPEntityDetections(sessionID string) error {
	return DB.Table("private.rdp_entity_detections").
		Where("session_id = ?", sessionID).
		Delete(&RDPEntityDetection{}).Error
}
