package models

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
)

const tableEvents = "private.events"

type Event struct {
	ID              string          `gorm:"column:id"`
	OrgID           string          `gorm:"column:org_id"`
	EventType       string          `gorm:"column:event_type"`
	Payload         json.RawMessage `gorm:"column:payload;serializer:json"`
	OccurredAt      time.Time       `gorm:"column:occurred_at"`
	Source          string          `gorm:"column:source"`
	ProducerEventID sql.NullString  `gorm:"column:producer_event_id"`
	CreatedAt       time.Time       `gorm:"column:created_at"`
}

// InsertEvent inserts a new event and returns its ID.
// On idempotency conflict (same org_id, event_type, producer_event_id),
// returns the existing row's ID.
// why the idempotency? because we want to avoid the same event being processed multiple times.
// this is not related to the event routing, but to processing the EVENT itself.
// so if the event is already processed, we don't want to process it again.
// retries will be handled by the event dispatcher.
func InsertEvent(tx *gorm.DB, event Event) (string, error) {
	id, _, err := UpsertEventIdempotent(tx, event)
	return id, err
}

// UpsertEventIdempotent inserts a new event or finds an existing one via the
// idempotency key (org_id, event_type, producer_event_id).
// Returns (eventID, isNew, error).
func UpsertEventIdempotent(tx *gorm.DB, event Event) (string, bool, error) {
	if !event.ProducerEventID.Valid || event.ProducerEventID.String == "" {
		if err := tx.Table(tableEvents).Create(&event).Error; err != nil {
			return "", false, err
		}
		return event.ID, true, nil
	}

	// Try insert; on conflict return existing row
	var resultID string
	err := tx.Raw(`
		INSERT INTO private.events (id, org_id, event_type, payload, occurred_at, source, producer_event_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (org_id, event_type, producer_event_id) WHERE producer_event_id IS NOT NULL
		DO NOTHING
		RETURNING id
	`, event.ID, event.OrgID, event.EventType, event.Payload,
		event.OccurredAt, event.Source, event.ProducerEventID, event.CreatedAt).
		Scan(&resultID).Error
	if err != nil {
		return "", false, err
	}

	if resultID != "" {
		return resultID, true, nil
	}

	// Conflict occurred — fetch existing
	err = tx.Table(tableEvents).
		Select("id").
		Where("org_id = ? AND event_type = ? AND producer_event_id = ?",
			event.OrgID, event.EventType, event.ProducerEventID).
		Scan(&resultID).Error
	if err != nil {
		return "", false, err
	}
	return resultID, false, nil
}

func GetEvent(orgID, id string) (*Event, error) {
	var event Event
	err := DB.Table(tableEvents).
		Where("org_id = ? AND id = ?", orgID, id).
		First(&event).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &event, nil
}

func ListEventsForOrg(orgID string, eventType string, limit, offset int) ([]*Event, int64, error) {
	q := DB.Table(tableEvents).Where("org_id = ?", orgID)
	if eventType != "" {
		q = q.Where("event_type = ?", eventType)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var items []*Event
	if err := q.Order("occurred_at DESC").
		Limit(limit).Offset(offset).
		Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}
