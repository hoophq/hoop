package models

import (
	"database/sql"

	"github.com/hoophq/hoop/common/log"
)

type UserGroup struct {
	OrgID            string
	UserID           string
	ServiceAccountId sql.NullString
	Name             string
}

func GetUserGroupsByOrgID(orgID string) ([]UserGroup, error) {
	log.Infof("listing user groups for org=%s", orgID)
	var userGroups []UserGroup
	if err := DB.Where("org_id = ?", orgID).Find(&userGroups).Error; err != nil {
		log.Errorf("failed to list user groups, reason=%v", err)
		return nil, err
	}

	return userGroups, nil
}

func GetUserGroupsByUserID(orgID, userID string) ([]UserGroup, error) {
	log.Infof("listing user groups for org=%s, user=%s", orgID, userID)
	var userGroups []UserGroup
	if err := DB.Where("org_id = ? AND user_id = ?", orgID, userID).Find(&userGroups).Error; err != nil {
		log.Errorf("failed to list user groups, reason=%v", err)
		return nil, err
	}

	return userGroups, nil
}

func InsertUserGroups(userGroups []UserGroup) error {
	log.Infof("inserting user groups")
	if len(userGroups) == 0 {
		return nil
	}
	if err := DB.Create(&userGroups).Error; err != nil {
		log.Errorf("failed to insert user groups, reason=%v", err)
		return err
	}

	return nil
}

func DeleteUserGroupsByUserID(userID string) error {
	log.Infof("deleting user groups for user=%s", userID)
	if err := DB.Where("user_id = ?", userID).Delete(&UserGroup{}).Error; err != nil {
		log.Errorf("failed to delete user groups, reason=%v", err)
		return err
	}

	return nil
}
