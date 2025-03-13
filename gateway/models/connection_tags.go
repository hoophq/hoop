package models

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const tableConnectionTags = "private.connection_tags"

type ConnectionTag struct {
	ID        string    `gorm:"column:id"`
	OrgID     string    `gorm:"column:org_id"`
	Key       string    `gorm:"column:key"`
	Value     string    `gorm:"column:value"`
	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

func CreateConnectionTag(obj *ConnectionTag) error {
	err := DB.Table(tableConnectionTags).Model(obj).Create(obj).Error
	if err == gorm.ErrDuplicatedKey {
		return ErrAlreadyExists
	}
	return err
}

func UpdateConnectionTagValue(orgID, id, val string) error {
	res := DB.Table(tableConnectionTags).
		Where("org_id = ? AND id = ?", orgID, id).
		Updates(ConnectionTag{
			Value:     val,
			UpdatedAt: time.Now().UTC(),
		})
	if res.Error == nil && res.RowsAffected == 0 {
		return ErrNotFound
	}
	return res.Error
}

func GetConnectionTagByID(orgID, id string) (*ConnectionTag, error) {
	var obj ConnectionTag
	if err := DB.Table(tableConnectionTags).Where("org_id = ? AND id = ?", orgID, id).
		First(&obj).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	return &obj, nil
}

func ListConnectionTags(orgID string) ([]ConnectionTag, error) {
	var items []ConnectionTag
	err := DB.Table(tableConnectionTags).
		Where("org_id = ?", orgID).Find(&items).Error
	if err != nil {
		return nil, err
	}
	return items, nil
}

// UpsertBatchConnectionTags create connection tags in batch
func UpsertBatchConnectionTags(items []ConnectionTag) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		for _, item := range items {
			err := tx.Table(tableConnectionTags).
				Model(ConnectionTag{}).
				Clauses(clause.OnConflict{DoNothing: true}).
				Create(item).
				Error
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func updateBatchConnectionTags(tx *gorm.DB, orgID, connID string, tags map[string]string) error {
	err := tx.Exec(
		`DELETE FROM private.connection_tags_association WHERE org_id = ? AND connection_id = ?`,
		orgID, connID).Error
	if err != nil {
		return fmt.Errorf("failed cleaning connection tags association, reason=%v", err)
	}
	for key, value := range tags {
		tagID := uuid.NewString()
		err := tx.Raw(`
			INSERT INTO private.connection_tags (org_id, id, key, value, updated_at)
			VALUES (?, ?, ?, ?, NOW())
				-- TODO: fix set update_at, it should not update nothing
				ON CONFLICT (org_id, key, value) DO UPDATE SET updated_at = NOW()
        		RETURNING id;
		`, orgID, tagID, key, value).Scan(&tagID).Error
		if err != nil {
			return fmt.Errorf("failed creating connection tags, reason=%v", err)
		}

		// assign tags to the connection
		associationID := uuid.NewString()
		err = tx.Exec(`
			INSERT INTO private.connection_tags_association (org_id, id, connection_id, tag_id)
			VALUES (?, ?, ?, ?)`, orgID, associationID, connID, tagID).
			Error
		if err != nil {
			return fmt.Errorf("failed creating connection tags association, reason=%v", err)
		}
	}
	return nil
}
