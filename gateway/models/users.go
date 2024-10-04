package models

import (
	"github.com/hoophq/hoop/common/log"
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

func GetUserBySubject(orgID, subject string) (User, error) {
	log.Debugf("getting user=%s for org=%s", subject, orgID)
	var user User
	if err := DB.Where("org_id = ? AND subject = ?", orgID, subject).First(&user).Error; err != nil {
		return User{}, err
	}

	return user, nil
}

func GetUserByEmail(orgID, email string) (User, error) {
	log.Debugf("getting user=%s for org=%s", email, orgID)
	var user User
	if err := DB.Where("org_id = ? AND email = ?", orgID, email).First(&user).Error; err != nil {
		return User{}, err
	}

	return user, nil
}

func GetUser(orgID, userID string) (User, error) {
	log.Debugf("getting user=%s for org=%s", userID, orgID)
	var user User
	if err := DB.Where("org_id = ? AND id = ?", orgID, userID).First(&user).Error; err != nil {
		return User{}, err
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

func UpdateUser(user User) error {
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
