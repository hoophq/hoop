package models

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// MCPOAuthFlow is a short-lived row backing the MCP connection OAuth login
// flow (see migrations/000100_mcp_oauth_flows.up.sql and
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
	// MCPOAuthFlowStatusConsumed marks a flow whose token has been handed to
	// the create page. The row lives on only so the connection it authorized
	// can adopt its refresh token when it is saved; the access token is
	// scrubbed at the same moment, so a consumed row holds no credential the
	// browser did not already receive.
	MCPOAuthFlowStatusConsumed = "consumed"
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

// PurgeStaleMCPOAuthFlows deletes flow rows older than the given age.
//
// A flow is normally consumed by the token endpoint, but a login the admin
// abandons (closes the popup, walks away) is never polled and would otherwise
// sit forever holding an encrypted PKCE verifier and, for a login that did
// complete, a token nobody will use. Called opportunistically from the
// authorize handler so the sweep costs no scheduler and no extra connection.
func PurgeStaleMCPOAuthFlows(db *gorm.DB, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan)
	res := db.Exec(`DELETE FROM private.mcp_oauth_flows WHERE created_at < ?`, cutoff)
	return res.RowsAffected, res.Error
}

// ConsumeMCPOAuthFlow marks a flow as redeemed and scrubs its access token.
//
// The row survives redemption because the connection it authorized does not
// exist yet: the grant that will renew this credential can only be created
// when that connection is saved (services.AdoptMCPOAuthGrant). Only the
// refresh token and the endpoint/client identity needed to use it stay behind,
// so the access token's exposure at rest ends exactly where it did when the
// row was deleted outright.
func ConsumeMCPOAuthFlow(db *gorm.DB, id string) error {
	return db.Table(mcpOAuthFlowsTable).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":                 MCPOAuthFlowStatusConsumed,
			"access_token_encrypted": nil,
		}).Error
}
