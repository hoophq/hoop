package models

import (
	"errors"

	"github.com/hoophq/hoop/common/log"
	"gorm.io/gorm"
)

type User struct {
	ID             string `gorm:"column:id"`
	OrgID          string `gorm:"column:org_id"`
	Subject        string `gorm:"column:subject"`
	Name           string `gorm:"column:name"`
	Picture        string `gorm:"column:picture"`
	Email          string `gorm:"column:email"`
	Verified       bool   `gorm:"column:verified"`
	Status         string `gorm:"column:status"`
	SlackID        string `gorm:"column:slack_id"`
	HashedPassword string `gorm:"column:hashed_password"`
}

func ListUsers(orgID string) ([]User, error) {
	var users []User
	if err := DB.Where("org_id = ?", orgID).Order("email desc").Find(&users).Error; err != nil {
		log.Errorf("failed to list users, reason=%v", err)
		return nil, err
	}

	return users, nil
}

func GetInvitedUserByEmail(email string) (*User, error) {
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
	var user *User
	if err := DB.Where("org_id = ? AND email = ?", orgID, email).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return user, nil
}

func GetInvitedUserByEmailAndOrg(email, orgID string) (*User, error) {
	var user *User
	if err := DB.Where("org_id = ? AND email = ? AND status = 'invited'", orgID, email).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return user, nil
}

func GetUserByEmail(email string) (*User, error) {
	var user *User
	if err := DB.Where("email = ?", email).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return user, nil
}

func ListUsersByEmail(email string) ([]User, error) {
	var users []User
	err := DB.Where("email = ?", email).Find(&users).Error
	return users, err
}

func CreateUser(user User) error {
	if err := DB.Create(&user).Error; err != nil {
		log.Errorf("failed to create user, reason=%v", err)
		return err
	}

	return nil
}

func UpdateUser(user *User) error {
	return DB.Save(&user).Error
}

func DeleteUser(orgID, subject string) error {
	return DB.
		Where("org_id = ? AND subject = ?", orgID, subject).
		Delete(&User{}).
		Error
}

func DeleteUserByID(id string) error {
	return DB.Where("id = ?", id).Delete(&User{}).Error
}

// PromoteInvitedUser atomically migrates a user to an invited org.
// It deletes the current user record first (releasing the UNIQUE subject constraint)
// then promotes the invited record by binding the subject and activating it.
func PromoteInvitedUser(invitedUserID, currentUserOrgID, idpSubject, name, picture string) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(
			`DELETE FROM private.users WHERE org_id = ? AND subject = ?`,
			currentUserOrgID, idpSubject,
		).Error; err != nil {
			return err
		}
		return tx.Exec(
			`UPDATE private.users SET status = 'active', verified = true, subject = ?, name = ?, picture = ? WHERE id = ? AND status = 'invited'`,
			idpSubject, name, picture, invitedUserID,
		).Error
	})
}

func DeletePendingInvitationByEmail(email string) error {
	return DB.Exec(`DELETE FROM private.users WHERE email = ? AND status = 'invited'`, email).Error
}

func UpdateUserAndUserGroups(user *User, userGroups []UserGroup) error {
	tx := DB.Begin()
	if err := tx.Save(&user).Error; err != nil {
		tx.Rollback()
		return err
	}

	// delete old user groups
	if err := tx.Where("user_id = ?", user.ID).Delete(&UserGroup{}).Error; err != nil {
		tx.Rollback()
		return err
	}

	// create new user groups
	if len(userGroups) > 0 {
		if err := tx.Create(&userGroups).Error; err != nil {
			tx.Rollback()
			return err
		}
	}

	return tx.Commit().Error
}
