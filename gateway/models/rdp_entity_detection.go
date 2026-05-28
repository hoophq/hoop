package models

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
