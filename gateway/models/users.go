package models

import (
	"errors"

	"github.com/hoophq/hoop/common/log"
	"gorm.io/gorm"
)

type User struct {
	ID             string
	OrgID          string
	Subject        string
	Name           string
	Picture        string
	Email          string
	Verified       bool
	Status         string
	SlackID        string
	HashedPassword string
}

func ListUsers(orgID string) ([]User, error) {
	log.Debugf("listing users for org=%s", orgID)
	var users []User
	if err := DB.Where("org_id = ?", orgID).Find(&users).Error; err != nil {
		log.Errorf("failed to list users, reason=%v", err)
		return nil, err
	}

	return users, nil
}

func GetInvitedUserByEmail(email string) (*User, error) {
	log.Debugf("getting invited user=%s", email)
	var user *User
	if err := DB.Where("email = ? AND status = ?", email, "invited").First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return user, nil
}

func GetUserByOrgIDAndSlackID(orgID, slackID string) (*User, error) {
	log.Debugf("getting user=%s for org=%s", slackID, orgID)
	var user *User
	if err := DB.Where("org_id = ? AND slack_id = ?", orgID, slackID).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return user, nil
}

func GetUserBySubjectAndOrg(subject, orgID string) (*User, error) {
	log.Debugf("getting user=%s for org=%s", subject, orgID)
	var user *User
	if err := DB.Where("org_id = ? AND subject = ?", orgID, subject).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return user, nil
}

func GetUserByEmailAndOrg(email, orgID string) (*User, error) {
	log.Debugf("getting user=%s for org=%s", email, orgID)
	var user *User
	if err := DB.Where("org_id = ? AND email = ?", orgID, email).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return user, nil
}

func GetUserByEmail(email string) (*User, error) {
	log.Debugf("getting user=%s", email)
	var user *User
	if err := DB.Where("email = ?", email).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return user, nil
}

func CreateUser(user User) error {
	log.Debugf("creating user=%s for org=%s", user.ID, user.OrgID)
	if err := DB.Create(&user).Error; err != nil {
		log.Errorf("failed to create user, reason=%v", err)
		return err
	}

	return nil
}

func UpdateUser(user *User) error {
	log.Debugf("updating user=%s for org=%s", user.ID, user.OrgID)
	if err := DB.Save(&user).Error; err != nil {
		log.Errorf("failed to update user, reason=%v", err)
		return err
	}

	return nil
}

func DeleteUser(orgID, subject string) error {
	log.Debugf("deleting user=%s for org=%s", subject, orgID)
	if err := DB.Where("org_id = ? AND subject = ?", orgID, subject).Delete(&User{}).Error; err != nil {
		log.Errorf("failed to delete user, reason=%v", err)
		return err
	}

	return nil
}

func UpdateUserAndUserGroups(user *User, userGroups []UserGroup) error {
	log.Debugf("updating user=%s for org=%s", user.ID, user.OrgID)
	tx := DB.Begin()
	if err := tx.Save(&user).Error; err != nil {
		tx.Rollback()
		log.Errorf("failed to update user, reason=%v", err)
		return err
	}

	// delete old user groups
	if err := tx.Where("user_id = ?", user.ID).Delete(&UserGroup{}).Error; err != nil {
		tx.Rollback()
		log.Errorf("failed to delete user groups, reason=%v", err)
		return err
	}

	// create new user groups
	if len(userGroups) > 0 {
		if err := tx.Create(&userGroups).Error; err != nil {
			tx.Rollback()
			log.Errorf("failed to create user groups, reason=%v", err)
			return err
		}
	}

	tx.Commit()
	return nil
}
