package models

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

type ConnectionCredentials struct {
	ID                 string     `gorm:"column:id"`
	OrgID              string     `gorm:"column:org_id"`
	UserSubject        string     `gorm:"column:user_subject"`
	ConnectionName     string     `gorm:"column:connection_name"`
	ConnectionType     string     `gorm:"column:connection_type"`
	SecretKeyHash      string     `gorm:"column:secret_key_hash"`
	SecretKey          *string    `gorm:"column:secret_key"`
	EncryptedSecretKey []byte     `gorm:"column:encrypted_secret_key"`
	SessionID          string     `gorm:"column:session_id"`
	CreatedAt          time.Time  `gorm:"column:created_at"`
	ExpireAt           time.Time  `gorm:"column:expire_at"`
	RevokedAt          *time.Time `gorm:"column:revoked_at"`
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
		Order("created_at DESC").
		First(&resp).
		Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}

		return nil, err
	}

	if resp.RevokedAt != nil {
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

// GetActiveCredentialByUserAndConnection returns the non-revoked credential for the
// given (org, user, connection) triple. Callers use this to implement stable-key
// reuse: the plaintext secret key is read from the secret_key column (falling
// back to the legacy encrypted_secret_key copy) and the expiration is refreshed
// in place.
// ErrNotFound is returned when the user does not yet have a credential for the
// connection or when the prior credential has been revoked.
func GetActiveCredentialByUserAndConnection(orgID, userSubject, connectionName string) (*ConnectionCredentials, error) {
	var resp ConnectionCredentials
	err := DB.Table("private.connection_credentials").
		Where("org_id = ? AND user_subject = ? AND connection_name = ? AND revoked_at IS NULL",
			orgID, userSubject, connectionName).
		Order("created_at DESC").
		First(&resp).
		Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &resp, err
}

// UpdateConnectionCredentialsSecret rotates the secret of an existing credential,
// storing both the hash (proxy auth lookup) and the plaintext (stable-key reuse).
// The legacy encrypted copy is cleared since it no longer matches the new secret.
func UpdateConnectionCredentialsSecret(id, secretKeyHash, secretKey string) error {
	return DB.Table("private.connection_credentials").
		Where("id = ?", id).
		Updates(map[string]any{
			"secret_key_hash":      secretKeyHash,
			"secret_key":           secretKey,
			"encrypted_secret_key": nil,
		}).Error
}

// BackfillConnectionCredentialsSecretKey stores the plaintext secret key on a
// row that predates plaintext storage (recovered from the legacy encrypted
// copy). Hash and encrypted copy are left untouched.
func BackfillConnectionCredentialsSecretKey(id, secretKey string) error {
	return DB.Table("private.connection_credentials").
		Where("id = ?", id).
		Update("secret_key", secretKey).Error
}

// RefreshCredentialExpiration updates the expiration and session_id of an
// existing credential. Used for stable-key reuse: the row keeps its id and
// stored secret key while each issuance refreshes the audit session and the
// validity window.
func RefreshCredentialExpiration(id, sessionID string, expireAt time.Time) error {
	return DB.Table("private.connection_credentials").
		Where("id = ?", id).
		Updates(map[string]any{
			"session_id": sessionID,
			"expire_at":  expireAt,
		}).Error
}

// ClearCredentialSession unlinks the credential from its audit session without
// invalidating the credential itself. Used by the "close session" endpoint so
// the user can reconnect later with the same token while the prior audit
// session is finalised.
func ClearCredentialSession(id string) error {
	return DB.Table("private.connection_credentials").
		Where("id = ?", id).
		Update("session_id", nil).Error
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

		_ = DB.Table("private.sessions").
			Where("id = ?", cred.SessionID).
			Update("status", "done").
			Update("ended_at", endTime).Error

		// Clear session_id so this record is not reprocessed on the next lazy call.
		// The credential row itself is preserved (with its stored secret key)
		// so a subsequent CreateConnectionCredentials call can reuse the same
		// token by re-arming expire_at and session_id via
		// RefreshCredentialExpiration.
		_ = DB.Table("private.connection_credentials").
			Where("id = ?", cred.ID).
			Update("session_id", nil).Error
	}

	return nil
}

// RevokeConnectionCredentials marks a credential as revoked. Because the
// stable-password contract can leave several rows sharing the same
// secret_key_hash (e.g. a review/Resume row plus the persistent row for the
// same user and connection), revocation burns the password itself: every
// non-revoked row with the same hash is marked, so neither proxy auth
// (GetValidConnectionCredentialsBySecretKey) nor stable-key reuse
// (GetActiveCredentialByUserAndConnection) can resurrect it. Revoked rows are
// kept for forensic queries. The next CreateConnectionCredentials call for the
// same (user, connection) generates a fresh row with a new key.
func RevokeConnectionCredentials(orgID, credentialID string) error {
	now := time.Now().UTC()
	return DB.Table("private.connection_credentials").
		Where(`org_id = ? AND revoked_at IS NULL AND secret_key_hash = (
			SELECT secret_key_hash FROM private.connection_credentials WHERE org_id = ? AND id = ?)`,
			orgID, orgID, credentialID).
		Updates(map[string]any{
			"revoked_at": now,
			// Also push expire_at into the past so any proxy that still reads
			// the row via the legacy expire_at check rejects new connections
			// immediately.
			"expire_at": now.Add(-time.Hour),
		}).Error
}

// ListConnectionCredentialsBySecretKeyHash returns every credential row sharing
// the given secret key hash. Used by revocation to tear down in-flight proxy
// sessions across all rows that share the burned password.
func ListConnectionCredentialsBySecretKeyHash(orgID, secretKeyHash string) ([]*ConnectionCredentials, error) {
	var resp []*ConnectionCredentials
	err := DB.Table("private.connection_credentials").
		Where("org_id = ? AND secret_key_hash = ? AND revoked_at IS NULL", orgID, secretKeyHash).
		Find(&resp).Error
	return resp, err
}

// HasRevokedCredentialWithHash reports whether any credential row carrying the
// given secret key hash has been revoked. Reuse paths call this to refuse
// re-issuing a burned password: revocation marks every sibling row sharing the
// hash, but rows revoked by the legacy single-row revoke can leave a
// non-revoked sibling still holding the dead password.
func HasRevokedCredentialWithHash(orgID, secretKeyHash string) (bool, error) {
	var count int64
	err := DB.Table("private.connection_credentials").
		Where("org_id = ? AND secret_key_hash = ? AND revoked_at IS NOT NULL", orgID, secretKeyHash).
		Count(&count).Error
	return count > 0, err
}
