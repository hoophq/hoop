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
	err := DB.Debug().Table("private.connection_credentials").
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

// CloseExpiredCredentialSessions closes sessions for expired connection credentials
// This is called lazily when accessing credentials or sessions
func CloseExpiredCredentialSessions() error {
	// Find all expired credentials with active sessions
	var expiredCreds []ConnectionCredentials
	err := DB.Table("private.connection_credentials").
		Where("expire_at < NOW() AND session_id IS NOT NULL AND session_id != ''").
		Find(&expiredCreds).Error
	
	if err != nil {
		return err
	}

	for _, cred := range expiredCreds {
		// Close the session
		endTime := time.Now().UTC()
		err := UpdateSessionEventStream(SessionDone{
			ID:         cred.SessionID,
			OrgID:      cred.OrgID,
			EndSession: &endTime,
			Status:     "done",
		})
		if err != nil {
			// Log but continue - don't fail the whole operation
			// Use fmt.Printf since we don't have log imported here
			continue
		}
	}

	return nil
}
