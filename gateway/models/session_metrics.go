package models

import (
	"database/sql"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type SessionMetrics struct {
	ID        string `gorm:"column:id;->"`
	OrgID     string `gorm:"column:org_id"`
	SessionID string `gorm:"column:session_id"`

	InfoType      string `gorm:"column:info_type"`
	CountMasked   int64  `gorm:"column:count_masked"`
	CountAnalyzed int64  `gorm:"column:count_analyzed"`

	ConnectionType    string         `gorm:"column:connection_type"`
	ConnectionSubtype sql.NullString `gorm:"column:connection_subtype"`

	SessionCreatedAt time.Time  `gorm:"column:session_created_at"`
	SessionEndedAt   *time.Time `gorm:"column:session_ended_at"`
}

func IncrementSessionMaskedMetrics(db *gorm.DB, sessionID string, maskedMetrics map[string]int64) error {
	if len(maskedMetrics) == 0 {
		return nil
	}

	session := &Session{}
	if err := db.Table("private.sessions").Where("id = ?", sessionID).First(session).Error; err != nil {
		return err
	}

	for infoType, count := range maskedMetrics {
		err := db.Table("private.session_metrics").Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "session_id"}, {Name: "info_type"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"count_masked":     gorm.Expr("session_metrics.count_masked + ?", count),
				"session_ended_at": session.EndSession,
			}),
		}).Create(&SessionMetrics{
			SessionID: session.ID,
			OrgID:     session.OrgID,

			InfoType:    infoType,
			CountMasked: count,

			ConnectionType:    session.ConnectionType,
			ConnectionSubtype: sql.NullString{String: session.ConnectionSubtype, Valid: session.ConnectionSubtype != ""},

			SessionCreatedAt: session.CreatedAt,
			SessionEndedAt:   session.EndSession,
		}).Error

		if err != nil {
			return err
		}
	}

	return nil
}

func IncrementSessionAnalyzedMetrics(db *gorm.DB, sessionID string, analyzedMetrics map[string]int64) error {
	if len(analyzedMetrics) == 0 {
		return nil
	}

	session := &Session{}
	if err := db.Table("private.sessions").Where("id = ?", sessionID).First(session).Error; err != nil {
		return err
	}

	for infoType, count := range analyzedMetrics {
		err := db.Table("private.session_metrics").Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "session_id"}, {Name: "info_type"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"count_analyzed":   gorm.Expr("session_metrics.count_analyzed + ?", count),
				"session_ended_at": session.EndSession,
			}),
		}).Create(&SessionMetrics{
			SessionID: session.ID,
			OrgID:     session.OrgID,

			InfoType:      infoType,
			CountAnalyzed: count,

			ConnectionType:    session.ConnectionType,
			ConnectionSubtype: sql.NullString{String: session.ConnectionSubtype, Valid: session.ConnectionSubtype != ""},

			SessionCreatedAt: session.CreatedAt,
			SessionEndedAt:   session.EndSession,
		}).Error

		if err != nil {
			return err
		}
	}

	return nil
}

func SetSessionMetricsEndedAt(db *gorm.DB, sessionID string) error {
	session := &Session{}
	if err := db.Table("private.sessions").Where("id = ?", sessionID).First(session).Error; err != nil {
		return err
	}

	if session.EndSession == nil {
		return nil
	}

	return db.
		Table("private.session_metrics").
		Where("session_id = ?", sessionID).
		Update("session_ended_at", session.EndSession).
		Error
}
