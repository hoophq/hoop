package models

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// FederationUserCredential is a per-user OAuth credential for the gcp_oauth
// federation provider. One row per (connection_id, user_id).
//
// RefreshTokenEncrypted is the AES-256-GCM ciphertext of the Google OAuth
// refresh token (encrypted with EncryptCredentialSecretKey). It is never
// returned to API callers in plaintext; it is only decrypted at session-open
// time inside the federation service. GoogleEmail is the consented Google
// identity and is stored in plaintext because it is not secret and surfaces as
// the resolved principal in session audit metadata.
type FederationUserCredential struct {
	ID                    string    `gorm:"column:id"`
	OrgID                 string    `gorm:"column:org_id"`
	ConnectionID          string    `gorm:"column:connection_id"`
	UserID                string    `gorm:"column:user_id"`
	UserEmail             string    `gorm:"column:user_email"`
	GoogleEmail           string    `gorm:"column:google_email"`
	RefreshTokenEncrypted []byte    `gorm:"column:refresh_token_encrypted"`
	Scopes                string    `gorm:"column:scopes"`
	CreatedAt             time.Time `gorm:"column:created_at"`
	UpdatedAt             time.Time `gorm:"column:updated_at"`
}

const federationUserCredentialsTable = "private.federation_user_credentials"

// GetFederationUserCredential returns the per-user OAuth credential for a
// connection. Returns ErrNotFound when the user has not connected an account
// for this connection (the caller surfaces this as an actionable "complete the
// consent flow" message rather than an internal error).
func GetFederationUserCredential(db *gorm.DB, orgID, connectionID, userID string) (*FederationUserCredential, error) {
	var resp FederationUserCredential
	err := db.Table(federationUserCredentialsTable).
		Where("org_id = ? AND connection_id = ? AND user_id = ?", orgID, connectionID, userID).
		First(&resp).
		Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// UpsertFederationUserCredential creates or updates the per-user OAuth
// credential for a connection. The unique constraint on (connection_id,
// user_id) collapses repeated consents into a single row; re-consenting
// refreshes the stored refresh token in place. UpdatedAt is refreshed on every
// write.
func UpsertFederationUserCredential(db *gorm.DB, cred *FederationUserCredential) error {
	cred.UpdatedAt = time.Now().UTC()
	if cred.CreatedAt.IsZero() {
		cred.CreatedAt = cred.UpdatedAt
	}
	return db.Exec(`
		INSERT INTO private.federation_user_credentials (
			id, org_id, connection_id, user_id, user_email,
			google_email, refresh_token_encrypted, scopes,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (connection_id, user_id) DO UPDATE SET
			user_email              = EXCLUDED.user_email,
			google_email            = EXCLUDED.google_email,
			refresh_token_encrypted = EXCLUDED.refresh_token_encrypted,
			scopes                  = EXCLUDED.scopes,
			updated_at              = EXCLUDED.updated_at
	`,
		cred.ID, cred.OrgID, cred.ConnectionID, cred.UserID, cred.UserEmail,
		cred.GoogleEmail, cred.RefreshTokenEncrypted, cred.Scopes,
		cred.CreatedAt, cred.UpdatedAt,
	).Error
}

// DeleteFederationUserCredential removes the per-user OAuth credential for a
// connection (the "disconnect Google account" action). Returns ErrNotFound
// when no row matched.
func DeleteFederationUserCredential(db *gorm.DB, orgID, connectionID, userID string) error {
	res := db.Exec(`
		DELETE FROM private.federation_user_credentials
		WHERE org_id = ? AND connection_id = ? AND user_id = ?
	`, orgID, connectionID, userID)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
