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
	log.Debugf("listing user groups for org=%s", orgID)
	var userGroups []UserGroup
	if err := DB.Where("org_id = ?", orgID).Find(&userGroups).Error; err != nil {
		log.Errorf("failed to list user groups, reason=%v", err)
		return nil, err
	}

	return userGroups, nil
}

func GetUserGroupsByUserID(userID string) ([]UserGroup, error) {
	log.Debugf("listing user groups for org=%s, user=%s", userID)
	var userGroups []UserGroup
	if err := DB.Where("user_id = ?", userID).Find(&userGroups).Error; err != nil {
		return nil, err
	}

	return userGroups, nil
}

func InsertUserGroups(userGroups []UserGroup) error {
	log.Debugf("inserting user groups")
	if len(userGroups) == 0 {
		return nil
	}

	return DB.Create(&userGroups).Error
}

func DeleteUserGroupsByUserID(userID string) error {
	log.Debugf("deleting user groups for user=%s", userID)
	return DB.Where("user_id = ?", userID).Delete(&UserGroup{}).Error
}
