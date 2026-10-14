package models

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type UserToken struct {
	UserID       string  `gorm:"column:user_id"`
	Token        string  `gorm:"column:token"`
	RefreshToken *string `gorm:"column:refresh_token"`
}

func GetUserToken(db *gorm.DB, userID string) (*UserToken, error) {
	var userToken UserToken

	err := db.Table("private.user_tokens").Where("user_id = ?", userID).First(&userToken).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}

		return nil, err
	}

	return &userToken, nil
}

func UpsertUserToken(db *gorm.DB, userID string, token string, refreshToken *string) error {
	userToken := UserToken{
		UserID:       userID,
		Token:        token,
		RefreshToken: refreshToken,
	}

	updateColumns := []string{"token"}
	if refreshToken != nil {
		updateColumns = append(updateColumns, "refresh_token")
	}

	tx := db.Table("private.user_tokens").Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns(updateColumns),
	}).Create(userToken)

	return tx.Error
}
