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

func DeleteUserGroupsByUserID(userID string) error {
	return DB.Where("user_id = ?", userID).Delete(&UserGroup{}).Error
}

// UpdateUserGroupName updates all instances of a group name in an organization
func UpdateUserGroupName(orgID string, oldName string, newName string) error {
	return DB.Exec(`
		UPDATE private.user_groups
		SET name = ?
		WHERE org_id = ? AND name = ?
	`, newName, orgID, oldName).Error
}

// DeleteUserGroup deletes all instances of a group from an organization
func DeleteUserGroup(orgID string, name string) error {
	return DB.Where("org_id = ? AND name = ?", orgID, name).Delete(&UserGroup{}).Error
}

// CreateUserGroupWithoutUser creates a group entry without binding it to any user
func CreateUserGroupWithoutUser(orgID string, name string) error {
	return DB.Exec(`
		INSERT INTO private.user_groups (org_id, name)
		VALUES (?, ?) ON CONFLICT DO NOTHING`,
		orgID, name,
	).Error
}
