package models

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type UserToken struct {
	UserID       string  `gorm:"column:user_id"`
	Token        string  `gorm:"column:token"`
	RefreshToken *string `gorm:"column:refresh_token"`
	// LastLoginAt is the time of the last token upsert: a sign-in or an OIDC refresh.
	LastLoginAt *time.Time `gorm:"column:last_login_at"`
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

// UpsertUserToken stores the token of a sign-in or an OIDC refresh. The same
// statement records the time as last_login_at, so no separate write can fail.
func UpsertUserToken(db *gorm.DB, userID string, token string, refreshToken *string) error {
	now := time.Now().UTC()
	userToken := UserToken{
		UserID:       userID,
		Token:        token,
		RefreshToken: refreshToken,
		LastLoginAt:  &now,
	}

	updateColumns := []string{"token", "last_login_at"}
	if refreshToken != nil {
		updateColumns = append(updateColumns, "refresh_token")
	}

	tx := db.Table("private.user_tokens").Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns(updateColumns),
	}).Create(userToken)

	return tx.Error
}
