package models

import (
	"fmt"
	"time"

	"github.com/lib/pq"
	"gorm.io/gorm"
)

type ServiceAccount struct {
	ID        string         `gorm:"column:id"`
	OrgID     string         `gorm:"column:org_id"`
	Subject   string         `gorm:"column:subject"`
	Name      string         `gorm:"column:name"`
	Groups    pq.StringArray `gorm:"column:groups;type:text[];->"`
	Status    string         `gorm:"column:status"`
	CreatedAt time.Time      `gorm:"column:created_at"`
	UpdatedAt time.Time      `gorm:"column:updated_at"`
}

func ListServiceAccounts(orgID string) ([]ServiceAccount, error) {
	var items []ServiceAccount
	return items, DB.Raw(`
	SELECT s.id, s.org_id, s.subject, s.name, s.status,
	COALESCE((
		SELECT array_agg(ug.name::TEXT) FROM private.user_groups ug
		WHERE ug.service_account_id = s.id
	), ARRAY[]::TEXT[]) AS groups,
	s.created_at, s.updated_at
	FROM private.service_accounts s
	WHERE org_id = ?`, orgID).
		Find(&items).
		Error
}

func CreateServiceAccount(sa *ServiceAccount) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		err := tx.Model(&ServiceAccount{}).Create(sa).Error
		if err != nil {
			if err == gorm.ErrDuplicatedKey {
				return ErrAlreadyExists
			}
			return err
		}
		for _, group := range sa.Groups {
			err = tx.Exec(`
			INSERT INTO private.user_groups (org_id, service_account_id, name)
			VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, sa.OrgID, sa.ID, group).
				Error
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func UpdateServiceAccount(sa *ServiceAccount) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(sa).Updates(
			ServiceAccount{
				Name:   sa.Name,
				Status: sa.Status,
			},
		)
		if res.Error != nil {
			return res.Error

		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		err := tx.Exec(`
			DELETE FROM private.user_groups
			WHERE org_id = ? AND service_account_id = ?`, sa.OrgID, sa.ID).
			Error
		if err != nil {
			return fmt.Errorf("failed to delete user group: %v", err)
		}
		for _, group := range sa.Groups {
			err = tx.Exec(`
			INSERT INTO private.user_groups (org_id, service_account_id, name)
			VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, sa.OrgID, sa.ID, group).
				Error
			if err != nil {
				return fmt.Errorf("failed to insert user group: %v", err)
			}
		}
		return nil
	})
}
