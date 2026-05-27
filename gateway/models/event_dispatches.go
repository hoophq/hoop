package models

import (
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const tableEventDispatches = "private.event_dispatches"

type EventDispatch struct {
	ID             string         `gorm:"column:id"`
	OrgID          string         `gorm:"column:org_id"`
	EventID        string         `gorm:"column:event_id"`
	SubscriptionID string         `gorm:"column:subscription_id"`
	Status         string         `gorm:"column:status"`
	Attempt        int            `gorm:"column:attempt"`
	SessionID      sql.NullString `gorm:"column:session_id"`
	LastError      sql.NullString `gorm:"column:last_error"`
	ReplayedFrom   sql.NullString `gorm:"column:replayed_from"`
	DispatchedAt   *time.Time     `gorm:"column:dispatched_at"`
	FinishedAt     *time.Time     `gorm:"column:finished_at"`
	CreatedAt      time.Time      `gorm:"column:created_at"`
}

type EventDispatchListItem struct {
	ID             string         `gorm:"column:id"`
	EventID        string         `gorm:"column:event_id"`
	EventType      string         `gorm:"column:event_type"`
	Status         string         `gorm:"column:status"`
	Attempt        int            `gorm:"column:attempt"`
	SessionID      sql.NullString `gorm:"column:session_id"`
	LastError      sql.NullString `gorm:"column:last_error"`
	ReplayedFrom   sql.NullString `gorm:"column:replayed_from"`
	OccurredAt     time.Time      `gorm:"column:occurred_at"`
	CreatedAt      time.Time      `gorm:"column:created_at"`
	DispatchedAt   *time.Time     `gorm:"column:dispatched_at"`
	FinishedAt     *time.Time     `gorm:"column:finished_at"`
}

func BulkInsertPendingDispatches(tx *gorm.DB, orgID, eventID string, subIDs []string) error {
	if len(subIDs) == 0 {
		return nil
	}
	dispatches := make([]EventDispatch, 0, len(subIDs))
	now := time.Now().UTC()
	for _, subID := range subIDs {
		dispatches = append(dispatches, EventDispatch{
			ID:             uuid.NewString(),
			OrgID:          orgID,
			EventID:        eventID,
			SubscriptionID: subID,
			Status:         "pending",
			Attempt:        0,
			CreatedAt:      now,
		})
	}
	return tx.Table(tableEventDispatches).Create(&dispatches).Error
}

func ListDispatchesForSubscription(orgID, subID string, limit, offset int) ([]*EventDispatchListItem, int64, error) {
	q := DB.Table(tableEventDispatches+" AS d").
		Select(`d.id, d.event_id, e.event_type, d.status, d.attempt,
			d.session_id, d.last_error, d.replayed_from,
			e.occurred_at, d.created_at, d.dispatched_at, d.finished_at`).
		Joins("JOIN private.events e ON e.id = d.event_id").
		Where("d.org_id = ? AND d.subscription_id = ?", orgID, subID)

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var items []*EventDispatchListItem
	if err := q.Order("d.created_at DESC").
		Limit(limit).Offset(offset).
		Scan(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func GetDispatchByID(orgID, id string) (*EventDispatch, error) {
	var d EventDispatch
	err := DB.Table(tableEventDispatches).
		Where("org_id = ? AND id = ?", orgID, id).
		First(&d).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &d, nil
}

func GetDispatchContext(db *gorm.DB, dispatchID string) (*EventSubscription, *Event, error) {
	var dispatch EventDispatch
	err := db.Table(tableEventDispatches).
		Where("id = ?", dispatchID).
		First(&dispatch).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, err
	}

	var sub EventSubscription
	if err := db.Table(tableEventSubscriptions).
		Where("id = ?", dispatch.SubscriptionID).
		First(&sub).Error; err != nil {
		return nil, nil, err
	}

	var event Event
	if err := db.Table(tableEvents).
		Where("id = ?", dispatch.EventID).
		First(&event).Error; err != nil {
		return nil, nil, err
	}

	return &sub, &event, nil
}

func ClaimNextDispatch(db *gorm.DB) (*EventDispatch, error) {
	var d EventDispatch
	err := db.Raw(`
		UPDATE private.event_dispatches
		SET status = 'processing', attempt = attempt + 1, dispatched_at = now()
		WHERE id = (
			SELECT id FROM private.event_dispatches
			WHERE status = 'pending'
			ORDER BY created_at ASC
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING *
	`).Scan(&d).Error
	if err != nil {
		return nil, err
	}
	if d.ID == "" {
		return nil, nil
	}
	return &d, nil
}

func MarkDispatchDelivered(db *gorm.DB, id string) error {
	return db.Table(tableEventDispatches).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":      "delivered",
			"finished_at": time.Now().UTC(),
		}).Error
}

func MarkDispatchFailed(db *gorm.DB, id, lastError string) error {
	return db.Table(tableEventDispatches).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":      "failed",
			"last_error":  lastError,
			"finished_at": time.Now().UTC(),
		}).Error
}

func SetDispatchSessionID(db *gorm.DB, id, sessionID string) error {
	return db.Table(tableEventDispatches).
		Where("id = ?", id).
		Update("session_id", sessionID).Error
}

func MarkOrphanedDispatchesFailed(db *gorm.DB) (int64, error) {
	res := db.Table(tableEventDispatches).
		Where("status = 'processing'").
		Updates(map[string]any{
			"status":      "failed",
			"last_error":  "gateway restart during processing — replay manually if intended",
			"finished_at": time.Now().UTC(),
		})
	return res.RowsAffected, res.Error
}

func CreateReplayDispatch(orgID string, original *EventDispatch) (*EventDispatch, error) {
	replay := EventDispatch{
		ID:             uuid.NewString(),
		OrgID:          orgID,
		EventID:        original.EventID,
		SubscriptionID: original.SubscriptionID,
		Status:         "pending",
		Attempt:        0,
		ReplayedFrom:   sql.NullString{String: original.ID, Valid: true},
		CreatedAt:      time.Now().UTC(),
	}
	if err := DB.Table(tableEventDispatches).Create(&replay).Error; err != nil {
		return nil, err
	}
	return &replay, nil
}

func GetDispatchStats(orgID, subID string, days int) (delivered int64, failed int64, lastError string, err error) {
	type statsResult struct {
		Delivered int64  `gorm:"column:delivered"`
		Failed    int64  `gorm:"column:failed"`
		LastError string `gorm:"column:last_error"`
	}
	var result statsResult
	err = DB.Raw(`
		SELECT
			COALESCE(SUM(CASE WHEN status = 'delivered' THEN 1 ELSE 0 END), 0) AS delivered,
			COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END), 0) AS failed,
			COALESCE(
				(SELECT last_error FROM private.event_dispatches
				 WHERE org_id = ? AND subscription_id = ? AND status = 'failed' AND last_error IS NOT NULL
				 ORDER BY finished_at DESC LIMIT 1),
				''
			) AS last_error
		FROM private.event_dispatches
		WHERE org_id = ? AND subscription_id = ? AND created_at >= now() - make_interval(days => ?)
	`, orgID, subID, orgID, subID, days).Scan(&result).Error
	if err != nil {
		return 0, 0, "", err
	}
	return result.Delivered, result.Failed, result.LastError, nil
}
