package models

import (
	"errors"
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
	SessionID      string    `gorm:"column:session_id"`
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
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &resp, err
}

// GetValidConnectionCredentialsBySecretKey retrieves a valid connection credential by its secret key hash.
// if a user has a valid connection credential, it could be used to connect in the requested resource
func GetValidConnectionCredentialsBySecretKey(connectionTypes []string, secretKeyHash string) (*ConnectionCredentials, error) {
	var resp ConnectionCredentials
	err := DB.Table("private.connection_credentials").
		Where("connection_type IN ? AND secret_key_hash = ?", connectionTypes, secretKeyHash).
		First(&resp).
		Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &resp, err
}

// GetConnectionByTypeAndID retrieves a connection credential by its type and ID
func GetConnectionByTypeAndID(connectionType, id string) (*ConnectionCredentials, error) {
	var resp ConnectionCredentials
	err := DB.Table("private.connection_credentials").
		Where("connection_type = ? AND id = ?", connectionType, id).
		First(&resp).
		Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &resp, err
}

// GetConnectionCredentialsBySessionID retrieves a connection credential by session ID
func GetConnectionCredentialsBySessionID(orgID, sessionID string) (*ConnectionCredentials, error) {
	var resp ConnectionCredentials
	err := DB.Table("private.connection_credentials").
		Where("org_id = ? AND session_id = ?", orgID, sessionID).
		First(&resp).
		Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &resp, err
}

// UpdateConnectionCredentialsSecretKey updates the secret key hash of an existing credential
func UpdateConnectionCredentialsSecretKey(id, secretKeyHash string) error {
	return DB.Table("private.connection_credentials").
		Where("id = ?", id).
		Update("secret_key_hash", secretKeyHash).Error
}

// CloseExpiredCredentialSessions closes sessions for expired connection credentials
// This is called lazily when accessing credentials or sessions
func CloseExpiredCredentialSessions() error {
	var expiredCreds []ConnectionCredentials
	err := DB.Table("private.connection_credentials").
		Where("expire_at < NOW() AND session_id IS NOT NULL AND session_id != ''").
		Find(&expiredCreds).Error
	if err != nil {
		return err
	}

	for _, cred := range expiredCreds {
		endTime := time.Now().UTC()
		_ = UpdateSessionEventStream(SessionDone{
			ID:         cred.SessionID,
			OrgID:      cred.OrgID,
			EndSession: &endTime,
			Status:     "done",
		})
		// Clear session_id so this record is not reprocessed on the next lazy call
		_ = DB.Table("private.connection_credentials").
			Where("id = ?", cred.ID).
			Update("session_id", nil).Error
	}

	return nil
}
