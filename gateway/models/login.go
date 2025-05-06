package models

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

type Login struct {
	ID        string    `gorm:"column:id"`
	Redirect  string    `gorm:"column:redirect"`
	Outcome   string    `gorm:"column:outcome"`
	SlackID   string    `gorm:"column:slack_id"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

func CreateLogin(login *Login) error {
	return DB.Table("private.login").Create(login).Error
}

// GetLoginByState retrieves a login record by its state ID
func GetLoginByState(stateID string) (*Login, error) {
	var login Login
	err := DB.Table("private.login").
		Where("id = ?", stateID).
		First(&login).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &login, nil
}

func UpdateLoginOutcome(stateID, outcome string) error {
	// Truncate outcome if it's too long (max 200 chars)
	if len(outcome) >= 200 {
		outcome = outcome[:195] + " ..."
	}

	return DB.Table("private.login").
		Where("id = ?", stateID).
		Updates(map[string]any{
			"outcome":    outcome,
			"updated_at": time.Now().UTC(),
		}).Error
}
