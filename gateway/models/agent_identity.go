package models

import (
	"fmt"
	"time"

	"github.com/lib/pq"
	"gorm.io/gorm"
)

type AgentIdentity struct {
	ID        string         `gorm:"column:id"`
	OrgID     string         `gorm:"column:org_id"`
	Subject   string         `gorm:"column:subject"`
	Name      string         `gorm:"column:name"`
	Status    string         `gorm:"column:status"`
	Groups    pq.StringArray `gorm:"column:groups;type:text[];->"`
	CreatedAt time.Time      `gorm:"column:created_at"`
	UpdatedAt time.Time      `gorm:"column:updated_at"`
}

func ListAgentIdentities(orgID string) ([]AgentIdentity, error) {
	var items []AgentIdentity
	return items, DB.Raw(`
	SELECT a.id, a.org_id, a.subject, a.name, a.status,
	COALESCE((
		SELECT array_agg(ug.name::TEXT) FROM private.user_groups ug
		WHERE ug.service_account_id = a.id
	), ARRAY[]::TEXT[]) AS groups,
	a.created_at, a.updated_at
	FROM private.agent_identities a
	WHERE a.org_id = ?`, orgID).
		Find(&items).
		Error
}

func GetAgentIdentityByID(orgID, id string) (*AgentIdentity, error) {
	var item AgentIdentity
	err := DB.Raw(`
	SELECT a.id, a.org_id, a.subject, a.name, a.status,
	COALESCE((
		SELECT array_agg(ug.name::TEXT) FROM private.user_groups ug
		WHERE ug.service_account_id = a.id
	), ARRAY[]::TEXT[]) AS groups,
	a.created_at, a.updated_at
	FROM private.agent_identities a
	WHERE a.org_id = ? AND a.id = ?`, orgID, id).
		Scan(&item).
		Error
	if err != nil {
		return nil, err
	}
	if item.ID == "" {
		return nil, ErrNotFound
	}
	return &item, nil
}

func CreateAgentIdentity(a *AgentIdentity) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		err := tx.Model(&AgentIdentity{}).Create(a).Error
		if err != nil {
			if err == gorm.ErrDuplicatedKey {
				return ErrAlreadyExists
			}
			return err
		}
		for _, group := range a.Groups {
			err = tx.Exec(`
			INSERT INTO private.user_groups (org_id, service_account_id, name)
			VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, a.OrgID, a.ID, group).
				Error
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func UpdateAgentIdentity(a *AgentIdentity) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(a).Updates(
			AgentIdentity{
				Name:   a.Name,
				Status: a.Status,
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
			WHERE org_id = ? AND service_account_id = ?`, a.OrgID, a.ID).
			Error
		if err != nil {
			return fmt.Errorf("failed to delete user groups: %v", err)
		}
		for _, group := range a.Groups {
			err = tx.Exec(`
			INSERT INTO private.user_groups (org_id, service_account_id, name)
			VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, a.OrgID, a.ID, group).
				Error
			if err != nil {
				return fmt.Errorf("failed to insert user group: %v", err)
			}
		}
		return nil
	})
}

func DeleteAgentIdentity(orgID, id string) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		err := tx.Exec(`
			DELETE FROM private.user_groups
			WHERE org_id = ? AND service_account_id = ?`, orgID, id).
			Error
		if err != nil {
			return fmt.Errorf("failed to delete user groups: %v", err)
		}
		res := tx.Exec(`DELETE FROM private.agent_identities WHERE org_id = ? AND id = ?`, orgID, id)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		return nil
	})
}
