package models

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/lib/pq"
	"gorm.io/gorm"
)

const tableEventSubscriptions = "private.event_subscriptions"

type EventSubscription struct {
	ID                string            `gorm:"column:id"`
	OrgID             string            `gorm:"column:org_id"`
	Name              string            `gorm:"column:name"`
	Description       string            `gorm:"column:description"`
	EventTypes        pq.StringArray    `gorm:"column:event_types;type:text[]"`
	RunbookRepository string            `gorm:"column:runbook_repository"`
	RunbookFile       string            `gorm:"column:runbook_file"`
	ConnectionName    string            `gorm:"column:connection_name"`
	ParameterMapping  map[string]string `gorm:"column:parameter_mapping;serializer:json"`
	Status            string            `gorm:"column:status"`
	CreatedByUserID   string            `gorm:"column:created_by_user_id"`
	CreatedByEmail    string            `gorm:"column:created_by_email"`
	CreatedByGroups   pq.StringArray    `gorm:"column:created_by_groups;type:text[]"`
	CreatedAt         time.Time         `gorm:"column:created_at"`
	UpdatedAt         time.Time         `gorm:"column:updated_at"`
}

func ListEventSubscriptions(orgID string, filterStatus string) ([]*EventSubscription, error) {
	var subs []*EventSubscription
	q := DB.Table(tableEventSubscriptions).Where("org_id = ?", orgID)
	if filterStatus != "" {
		q = q.Where("status = ?", filterStatus)
	}
	if err := q.Order("created_at DESC").Find(&subs).Error; err != nil {
		return nil, err
	}
	return subs, nil
}

func GetEventSubscriptionByID(orgID, id string) (*EventSubscription, error) {
	var sub EventSubscription
	err := DB.Table(tableEventSubscriptions).
		Where("org_id = ? AND id = ?", orgID, id).
		First(&sub).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &sub, nil
}

func GetEventSubscriptionByName(orgID, name string) (*EventSubscription, error) {
	var sub EventSubscription
	err := DB.Table(tableEventSubscriptions).
		Where("org_id = ? AND name = ?", orgID, name).
		First(&sub).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &sub, nil
}

func CreateEventSubscription(sub *EventSubscription) error {
	err := DB.Table(tableEventSubscriptions).Create(sub).Error
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return ErrAlreadyExists
		}
		return err
	}
	return nil
}

func UpdateEventSubscription(sub *EventSubscription) error {
	mappingJSON, err := json.Marshal(sub.ParameterMapping)
	if err != nil {
		return err
	}
	res := DB.Table(tableEventSubscriptions).
		Where("org_id = ? AND id = ?", sub.OrgID, sub.ID).
		Updates(map[string]any{
			"name":              sub.Name,
			"description":       sub.Description,
			"event_types":       sub.EventTypes,
			"runbook_repository": sub.RunbookRepository,
			"runbook_file":      sub.RunbookFile,
			"connection_name":   sub.ConnectionName,
			"parameter_mapping": json.RawMessage(mappingJSON),
			"updated_at":        sub.UpdatedAt,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func DeleteEventSubscription(orgID, id string) error {
	res := DB.Table(tableEventSubscriptions).
		Where("org_id = ? AND id = ?", orgID, id).
		Delete(&EventSubscription{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func SetEventSubscriptionStatus(orgID, id, status string) error {
	res := DB.Table(tableEventSubscriptions).
		Where("org_id = ? AND id = ?", orgID, id).
		Updates(map[string]any{
			"status":     status,
			"updated_at": time.Now().UTC(),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func ListActiveSubscriptionIDsForEvent(tx *gorm.DB, orgID, eventType string) ([]string, error) {
	var ids []string
	err := tx.Table(tableEventSubscriptions).
		Select("id").
		Where("org_id = ? AND status = 'active' AND event_types @> ARRAY[?]", orgID, eventType).
		Pluck("id", &ids).Error
	return ids, err
}
