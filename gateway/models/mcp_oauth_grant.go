package models

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// MCPOAuthGrant is a durable OAuth grant for an MCP Gateway (mcpproxy)
// connection (see migrations/000106_mcp_oauth_grants.up.sql).
//
// It is the counterpart of MCPOAuthFlow: the flow row carries the login across
// its three request hops and is consumed on read, while the grant survives to
// be refreshed. A completed flow is adopted into a grant when the connection
// it authorized is saved; the session-open path then renews the access token
// from the stored refresh token instead of shipping a frozen header that dies
// at the provider's TTL.
//
// UserID is empty for a connection-wide grant, which is what the current UI
// produces: one admin authorizes and every user of the connection shares the
// credential. Per-user grants are a second row under the same connection.
//
// Secret columns hold AES-256-GCM ciphertext produced by
// EncryptCredentialSecretKey, never plaintext.
type MCPOAuthGrant struct {
	ID                    string     `gorm:"column:id"`
	OrgID                 string     `gorm:"column:org_id"`
	ConnectionID          string     `gorm:"column:connection_id"`
	UserID                string     `gorm:"column:user_id"`
	ServerURL             string     `gorm:"column:server_url"`
	Resource              string     `gorm:"column:resource"`
	Issuer                string     `gorm:"column:issuer"`
	TokenEndpoint         string     `gorm:"column:token_endpoint"`
	ClientID              string     `gorm:"column:client_id"`
	ClientSecretEncrypted []byte     `gorm:"column:client_secret_encrypted"`
	TokenAuthMethod       string     `gorm:"column:token_auth_method"`
	Scopes                string     `gorm:"column:scopes"`
	AccessTokenEncrypted  []byte     `gorm:"column:access_token_encrypted"`
	RefreshTokenEncrypted []byte     `gorm:"column:refresh_token_encrypted"`
	TokenType             string     `gorm:"column:token_type"`
	TokenExpiresAt        *time.Time `gorm:"column:token_expires_at"`
	CreatedAt             time.Time  `gorm:"column:created_at"`
	UpdatedAt             time.Time  `gorm:"column:updated_at"`
}

const mcpOAuthGrantsTable = "private.mcp_oauth_grants"

// UpsertMCPOAuthGrant creates or replaces the grant for a (connection, user)
// pair. Re-authorizing a connection overwrites the previous grant rather than
// accumulating rows, so the unique constraint carries the identity.
func UpsertMCPOAuthGrant(db *gorm.DB, grant *MCPOAuthGrant) error {
	grant.UpdatedAt = time.Now().UTC()
	if grant.CreatedAt.IsZero() {
		grant.CreatedAt = grant.UpdatedAt
	}
	return db.Exec(`
		INSERT INTO private.mcp_oauth_grants (
			id, org_id, connection_id, user_id,
			server_url, resource, issuer, token_endpoint,
			client_id, client_secret_encrypted, token_auth_method, scopes,
			access_token_encrypted, refresh_token_encrypted, token_type, token_expires_at,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (connection_id, user_id) DO UPDATE SET
			server_url              = EXCLUDED.server_url,
			resource                = EXCLUDED.resource,
			issuer                  = EXCLUDED.issuer,
			token_endpoint          = EXCLUDED.token_endpoint,
			client_id               = EXCLUDED.client_id,
			client_secret_encrypted = EXCLUDED.client_secret_encrypted,
			token_auth_method       = EXCLUDED.token_auth_method,
			scopes                  = EXCLUDED.scopes,
			access_token_encrypted  = EXCLUDED.access_token_encrypted,
			refresh_token_encrypted = EXCLUDED.refresh_token_encrypted,
			token_type              = EXCLUDED.token_type,
			token_expires_at        = EXCLUDED.token_expires_at,
			updated_at              = EXCLUDED.updated_at
	`,
		grant.ID, grant.OrgID, grant.ConnectionID, grant.UserID,
		grant.ServerURL, grant.Resource, grant.Issuer, grant.TokenEndpoint,
		grant.ClientID, grant.ClientSecretEncrypted, grant.TokenAuthMethod, grant.Scopes,
		grant.AccessTokenEncrypted, grant.RefreshTokenEncrypted, grant.TokenType, grant.TokenExpiresAt,
		grant.CreatedAt, grant.UpdatedAt,
	).Error
}

// GetMCPOAuthGrant returns the grant for a (connection, user) pair without
// locking. Use LockMCPOAuthGrant on any path that may write the row back.
func GetMCPOAuthGrant(db *gorm.DB, orgID, connectionID, userID string) (*MCPOAuthGrant, error) {
	var resp MCPOAuthGrant
	err := db.Table(mcpOAuthGrantsTable).
		Where("org_id = ? AND connection_id = ? AND user_id = ?", orgID, connectionID, userID).
		First(&resp).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// LockMCPOAuthGrant reads the grant FOR UPDATE inside the caller's
// transaction.
//
// The read-refresh-write cycle must be atomic across gateway replicas: an
// authorization server that rotates refresh tokens invalidates the old one on
// use, so two replicas refreshing the same grant concurrently would leave the
// loser holding a dead token and break the grant permanently. The row lock is
// what serializes them — the second replica blocks here and then finds the
// token the first one persisted, already valid.
//
// The lock is held until the caller's transaction commits, so callers must
// keep that transaction short.
func LockMCPOAuthGrant(tx *gorm.DB, orgID, connectionID, userID string) (*MCPOAuthGrant, error) {
	var resp MCPOAuthGrant
	err := tx.Raw(`
		SELECT * FROM private.mcp_oauth_grants
		WHERE org_id = ? AND connection_id = ? AND user_id = ?
		FOR UPDATE
	`, orgID, connectionID, userID).First(&resp).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// UpdateMCPOAuthGrantToken persists a renewed token on an existing grant. Only
// the token columns move: the endpoint and client identity that minted the
// grant are immutable for its lifetime.
func UpdateMCPOAuthGrantToken(db *gorm.DB, grant *MCPOAuthGrant) error {
	return db.Table(mcpOAuthGrantsTable).
		Where("id = ?", grant.ID).
		Updates(map[string]any{
			"access_token_encrypted":  grant.AccessTokenEncrypted,
			"refresh_token_encrypted": grant.RefreshTokenEncrypted,
			"token_type":              grant.TokenType,
			"token_expires_at":        grant.TokenExpiresAt,
			"updated_at":              time.Now().UTC(),
		}).Error
}

// DeleteMCPOAuthGrant removes a grant. Called when the authorization server
// rejects the stored refresh token, so the next session reports a clean
// "not authorized" instead of retrying a dead grant.
//
// Pass a plain handle, not the transaction that discovered the rejection.
// That transaction unwinds — reporting the rejection is what fails it — and
// would take this DELETE with it, leaving the dead grant in place to be
// replayed at every session open (see services.ResolveMCPOAuthHeader, which
// carries the rejection out as a sentinel and deletes afterwards).
func DeleteMCPOAuthGrant(db *gorm.DB, id string) error {
	return db.Exec(`DELETE FROM private.mcp_oauth_grants WHERE id = ?`, id).Error
}
