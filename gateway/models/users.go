package models

import (
	"errors"
	"time"

	"github.com/google/uuid"
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

type UserOrganization struct {
	ID        string    `gorm:"column:id"`
	UserID    string    `gorm:"column:user_id"`
	OrgID     string    `gorm:"column:org_id"`
	Role      string    `gorm:"column:role"`
	CreatedAt time.Time `gorm:"column:created_at"`
}

type UserPreference struct {
	ID          string    `gorm:"column:id"`
	UserID      string    `gorm:"column:user_id"`
	ActiveOrgID string    `gorm:"column:active_org_id"`
	UpdatedAt   time.Time `gorm:"column:updated_at"`
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

// ListUserOrganizations returns all organizations for a user
func ListUserOrganizations(userID string) ([]UserOrganization, error) {
	var orgs []UserOrganization
	if err := DB.Where("user_id = ?", userID).Find(&orgs).Error; err != nil {
		log.Errorf("failed to list user organizations, reason=%v", err)
		return nil, err
	}
	return orgs, nil
}

// GetUserPreference returns the user preference including active organization
func GetUserPreference(userID string) (*UserPreference, error) {
	var pref UserPreference
	if err := DB.Where("user_id = ?", userID).First(&pref).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &pref, nil
}

// SetActiveOrganization sets the active organization for a user
func SetActiveOrganization(userID, orgID string) error {
	// Check if the user belongs to the organization
	var count int64
	err := DB.Model(&UserOrganization{}).
		Where("user_id = ? AND org_id = ?", userID, orgID).
		Count(&count).Error
	if err != nil {
		return err
	}
	if count == 0 {
		return errors.New("user does not belong to this organization")
	}
	
	// Check if preference already exists
	var pref UserPreference
	err = DB.Where("user_id = ?", userID).First(&pref).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	
	// Create or update the preference
	if errors.Is(err, gorm.ErrRecordNotFound) {
		pref = UserPreference{
			ID:          uuid.NewString(),
			UserID:      userID,
			ActiveOrgID: orgID,
			UpdatedAt:   time.Now(),
		}
		return DB.Create(&pref).Error
	}
	
	// Update existing preference
	pref.ActiveOrgID = orgID
	pref.UpdatedAt = time.Now()
	return DB.Save(&pref).Error
}

// AddUserToOrganization adds a user to an organization
func AddUserToOrganization(userID, orgID, role string) error {
	// Check if the relationship already exists
	var count int64
	err := DB.Model(&UserOrganization{}).
		Where("user_id = ? AND org_id = ?", userID, orgID).
		Count(&count).Error
	if err != nil {
		return err
	}
	if count > 0 {
		return nil // Already exists
	}
	
	// Create the relationship
	userOrg := UserOrganization{
		ID:        uuid.NewString(),
		UserID:    userID,
		OrgID:     orgID,
		Role:      role,
		CreatedAt: time.Now(),
	}
	return DB.Create(&userOrg).Error
}

// RemoveUserFromOrganization removes a user from an organization
func RemoveUserFromOrganization(userID, orgID string) error {
	return DB.Where("user_id = ? AND org_id = ?", userID, orgID).
		Delete(&UserOrganization{}).
		Error
}
