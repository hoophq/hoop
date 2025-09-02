package models

import (
	"time"

	"gorm.io/gorm"
)

type ConnectionCredentials struct {
	ID             string    `gorm:"column:id"`
	OrgID          string    `gorm:"column:org_id"`
	UserSubject    string    `gorm:"column:user_subject"`
	ConnectionName string    `gorm:"column:connection_name"`
	ConnectionType string    `gorm:"column:connection_type"`
	SecretKeyHash  string    `gorm:"column:secret_key_hash"`
	CreatedAt      time.Time `gorm:"column:created_at"`
	ExpireAt       time.Time `gorm:"column:expire_at"`
}

func CreateConnectionCredentials(db *ConnectionCredentials) (*ConnectionCredentials, error) {
	return db, DB.Table("private.connection_credentials").Create(db).Error
}

func GetConnectionCredentialsByID(orgID, id string) (*ConnectionCredentials, error) {
	var resp ConnectionCredentials
	err := DB.Table("private.connection_credentials").
		Where("org_id = ? AND id = ?", orgID, id).
		First(&resp).
		Error
	if err == gorm.ErrRecordNotFound {
		return nil, ErrNotFound
	}
	return &resp, err
}

// GetValidConnectionCredentialsBySecretKey retrieves a valid connection credential by its secret key hash.
// if a user has a valid connection credential, it could be used to connect in the requested resource
func GetValidConnectionCredentialsBySecretKey(connectionType, secretKeyHash string) (*ConnectionCredentials, error) {
	var resp ConnectionCredentials
	err := DB.Table("private.connection_credentials").
		Where("connection_type = ? AND secret_key_hash = ?", connectionType, secretKeyHash).
		First(&resp).
		Error
	if err == gorm.ErrRecordNotFound {
		return nil, ErrNotFound
	}
	return &resp, err
}
