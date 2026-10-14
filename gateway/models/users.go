package models

import (
	"errors"
	"strings"
	"time"

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

// UserListOption filters ListUsersWithLastLogin. A zero Page returns every match.
type UserListOption struct {
	// Email matches part of the user email, ignoring case.
	Email string
	// Status matches the user status exactly.
	Status   string
	Page     int
	PageSize int
}

// UserWithLastLogin is a user plus the time of its last sign-in, NULL when
// none was recorded.
type UserWithLastLogin struct {
	User
	LastLoginAt *time.Time `gorm:"column:last_login_at"`
}

// userLikeEscaper neutralizes the LIKE wildcards in a partial-match filter.
var userLikeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// ListUsersWithLastLogin returns the org users that match opts and the count of
// all matches. A page sorts by name, then email; without a page the order is
// the one ListUsers uses.
func ListUsersWithLastLogin(db *gorm.DB, orgID string, opts UserListOption) ([]UserWithLastLogin, int64, error) {
	matches := func() *gorm.DB {
		q := db.Table("private.users AS u").Where("u.org_id = ?", orgID)
		if opts.Email != "" {
			q = q.Where("u.email ILIKE ?", "%"+userLikeEscaper.Replace(opts.Email)+"%")
		}
		if opts.Status != "" {
			q = q.Where("u.status::TEXT = ?", opts.Status)
		}
		return q
	}

	var total int64
	if err := matches().Count(&total).Error; err != nil {
		return nil, 0, err
	}

	q := matches().
		Select("u.id, u.org_id, u.subject, u.name, u.picture, u.email, u.verified, u.status, u.slack_id, ut.last_login_at").
		Joins("LEFT JOIN private.user_tokens AS ut ON ut.user_id = u.subject")
	if opts.Page > 0 {
		q = q.Order("LOWER(u.name), LOWER(u.email), u.id").
			Limit(opts.PageSize).
			Offset((opts.Page - 1) * opts.PageSize)
	} else {
		q = q.Order("u.email DESC")
	}

	var users []UserWithLastLogin
	if err := q.Find(&users).Error; err != nil {
		return nil, 0, err
	}
	return users, total, nil
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

// ApproverStatuses are the user statuses that may settle a review from Slack.
// An invited user was added by an admin and has not logged in yet.
var ApproverStatuses = []string{"active", "invited"}

// ListApproverUsersByEmailAndOrg returns every user of the org with this email
// and an approver status, ignoring case. It returns all of them, not the
// first: users.email has no unique index, and a caller that must identify one
// person has to see a duplicate to refuse it.
func ListApproverUsersByEmailAndOrg(db *gorm.DB, orgID, email string) ([]User, error) {
	var users []User
	err := db.Where("org_id = ? AND lower(email) = lower(?) AND status IN ?", orgID, email, ApproverStatuses).
		Order("id").
		Find(&users).Error
	return users, err
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
	err := DB.Where("email = ?", email).
		Order("CASE WHEN status = 'active' THEN 0 WHEN status = 'invited' THEN 1 ELSE 2 END, id").
		First(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return user, nil
}

func GetUserBySubject(subject string) (*User, error) {
	var user *User
	err := DB.Where("subject = ?", subject).
		Order("CASE WHEN status = 'active' THEN 0 WHEN status = 'invited' THEN 1 ELSE 2 END, id").
		First(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return user, nil
}

func GetUserByID(id string) (*User, error) {
	var user *User
	err := DB.Where("id = ?", id).First(&user).Error
	if err != nil {
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
