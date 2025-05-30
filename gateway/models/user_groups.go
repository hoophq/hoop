package models

import (
	"database/sql"

	"github.com/hoophq/hoop/common/log"
	"gorm.io/gorm"
)

type UserGroup struct {
	OrgID            string
	UserID           string
	ServiceAccountId sql.NullString
	Name             string
}

func GetUserGroupsByOrgID(orgID string) ([]UserGroup, error) {
	var userGroups []UserGroup
	if err := DB.Where("org_id = ?", orgID).Find(&userGroups).Error; err != nil {
		log.Errorf("failed to list user groups, reason=%v", err)
		return nil, err
	}
	return userGroups, nil
}

func GetUserGroupsByUserID(userID string) ([]UserGroup, error) {
	var userGroups []UserGroup
	if err := DB.Where("user_id = ?", userID).Find(&userGroups).Error; err != nil {
		return nil, err
	}
	return userGroups, nil
}

func InsertUserGroups(userGroups []UserGroup) error {
	if len(userGroups) == 0 {
		return nil
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		for _, ug := range userGroups {
			err := tx.Exec(`
				INSERT INTO private.user_groups (org_id, user_id, name)
				VALUES (?, ?, ?) ON CONFLICT DO NOTHING`,
				ug.OrgID, ug.UserID, ug.Name,
			).Error
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// DeleteUserGroup deletes all instances of a group from an organization
func DeleteUserGroup(orgID string, name string) error {
	result := DB.Where("org_id = ? AND name = ?", orgID, name).Delete(&UserGroup{})
	if result.Error != nil {
		return result.Error
	}

	if result.RowsAffected == 0 {
		return ErrNotFound
	}

	return nil
}

// CreateUserGroupWithoutUser creates a group entry without binding it to any user
func CreateUserGroupWithoutUser(orgID string, name string) error {
	// Check if a group with the same name already exists in this org
	var count int64
	err := DB.Table("private.user_groups").
		Where("org_id = ? AND name = ?", orgID, name).
		Count(&count).Error

	if err != nil {
		return err
	}

	if count > 0 {
		return ErrAlreadyExists
	}

	// Create the group if it doesn't exist
	return DB.Exec(`
		INSERT INTO private.user_groups (org_id, name)
		VALUES (?, ?)`,
		orgID, name,
	).Error
}
