package models

import (
	"github.com/lib/pq"
	"gorm.io/gorm"
)

type UserV2 struct {
	ID             string         `gorm:"column:id"`
	OrgID          string         `gorm:"column:org_id"`
	Subject        string         `gorm:"column:subject"`
	Email          string         `gorm:"column:email"`
	Name           string         `gorm:"column:name"`
	Verified       bool           `gorm:"column:verified"`
	Status         string         `gorm:"column:status"`
	Groups         pq.StringArray `gorm:"column:groups;type:text[];->"`
	SlackID        *string        `gorm:"column:slack_id"`
	Picture        *string        `gorm:"column:picture"`
	HashedPassword *string        `gorm:"column:hashed_password"`
}

func GetUserByEmailV2(email string) (*UserV2, error) {
	var user *UserV2
	err := DB.Raw(`
	SELECT
		id, org_id, subject, email, name, verified, status,
		COALESCE((
			SELECT array_agg(ug.name::TEXT) FROM private.user_groups ug
			WHERE ug.user_id = u.id OR ug.service_account_id = u.id
		), ARRAY[]::TEXT[]) AS groups,
		slack_id, picture, hashed_password
	FROM private.users u
	WHERE u.email = ?`, email).
		First(&user).Error
	if err == gorm.ErrRecordNotFound {
		return nil, ErrNotFound
	}
	return user, nil
}

func UpsertUserV2(user *UserV2) error {
	// Use a transaction to ensure atomicity
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Table("private.users").Save(&user).Error; err != nil {
			return err
		}

		if err := tx.Exec(`DELETE FROM private.user_groups WHERE user_id = ? AND org_id = ?`,
			user.ID, user.OrgID).Error; err != nil {
			return err
		}

		for _, groupName := range user.Groups {
			if err := tx.Exec(`
				INSERT INTO private.user_groups (user_id, org_id, name)
				VALUES (?, ?, ?)`, user.ID, user.OrgID, groupName).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
