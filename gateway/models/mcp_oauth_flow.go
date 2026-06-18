package models

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// MCPOAuthFlow is a short-lived row backing the MCP connection OAuth login
// flow (see rootfs/app/migrations/000099_mcp_oauth_flows.up.sql and
// api/connections/connection_mcp_oauth.go). The authorize endpoint creates it
// keyed by a random UUID (the OAuth "state" parameter); the callback endpoint
// updates it with the obtained token; the token endpoint reads it once and
// deletes it. Rows are single-use and TTL-bounded by the callback handler.
//
// Secret columns hold AES-256-GCM ciphertext produced by
// EncryptCredentialSecretKey (the credential vault), never plaintext.
type MCPOAuthFlow struct {
	ID                    string     `gorm:"column:id"`
	OrgID                 string     `gorm:"column:org_id"`
	UserID                string     `gorm:"column:user_id"`
	ServerURL             string     `gorm:"column:server_url"`
	Resource              string     `gorm:"column:resource"`
	Issuer                string     `gorm:"column:issuer"`
	AuthorizationEndpoint string     `gorm:"column:authorization_endpoint"`
	TokenEndpoint         string     `gorm:"column:token_endpoint"`
	ClientID              string     `gorm:"column:client_id"`
	ClientSecretEncrypted []byte     `gorm:"column:client_secret_encrypted"`
	TokenAuthMethod       string     `gorm:"column:token_auth_method"`
	CodeVerifierEncrypted []byte     `gorm:"column:code_verifier_encrypted"`
	Scopes                string     `gorm:"column:scopes"`
	RedirectURL           string     `gorm:"column:redirect_url"`
	Status                string     `gorm:"column:status"`
	ErrorReason           string     `gorm:"column:error_reason"`
	AccessTokenEncrypted  []byte     `gorm:"column:access_token_encrypted"`
	RefreshTokenEncrypted []byte     `gorm:"column:refresh_token_encrypted"`
	TokenType             string     `gorm:"column:token_type"`
	TokenExpiresAt        *time.Time `gorm:"column:token_expires_at"`
	CreatedAt             time.Time  `gorm:"column:created_at"`
}

// MCP OAuth flow status values.
const (
	MCPOAuthFlowStatusPending   = "pending"
	MCPOAuthFlowStatusCompleted = "completed"
	MCPOAuthFlowStatusError     = "error"
)

const mcpOAuthFlowsTable = "private.mcp_oauth_flows"

// CreateMCPOAuthFlow persists a new pending OAuth flow row.
func CreateMCPOAuthFlow(db *gorm.DB, flow *MCPOAuthFlow) error {
	if flow.CreatedAt.IsZero() {
		flow.CreatedAt = time.Now().UTC()
	}
	if flow.Status == "" {
		flow.Status = MCPOAuthFlowStatusPending
	}
	return db.Table(mcpOAuthFlowsTable).Create(flow).Error
}

// GetMCPOAuthFlow retrieves an OAuth flow row by its UUID (the OAuth state).
// Returns ErrNotFound when the flow is unknown, already consumed, or forged.
func GetMCPOAuthFlow(db *gorm.DB, id string) (*MCPOAuthFlow, error) {
	var resp MCPOAuthFlow
	err := db.Table(mcpOAuthFlowsTable).Where("id = ?", id).First(&resp).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// UpdateMCPOAuthFlowResult records the outcome of the token exchange on the
// flow row. The callback handler calls this with status completed (token
// columns populated) or error (error_reason populated).
func UpdateMCPOAuthFlowResult(db *gorm.DB, flow *MCPOAuthFlow) error {
	return db.Table(mcpOAuthFlowsTable).
		Where("id = ?", flow.ID).
		Updates(map[string]any{
			"status":                  flow.Status,
			"error_reason":            flow.ErrorReason,
			"access_token_encrypted":  flow.AccessTokenEncrypted,
			"refresh_token_encrypted": flow.RefreshTokenEncrypted,
			"token_type":              flow.TokenType,
			"token_expires_at":        flow.TokenExpiresAt,
		}).Error
}

// DeleteMCPOAuthFlow removes a flow row. Called once the token endpoint has
// returned the obtained token so a flow is single use.
func DeleteMCPOAuthFlow(db *gorm.DB, id string) error {
	return db.Exec(`DELETE FROM private.mcp_oauth_flows WHERE id = ?`, id).Error
}
