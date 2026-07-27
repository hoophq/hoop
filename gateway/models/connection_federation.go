package models

import (
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
)

// Hook source constants for ConnectionFederationConfig.HookSource. v1 ships a
// single source ("builtin") that dispatches to a built-in resolver. The
// constant exists so new sources can be added in future releases without
// touching call sites that switch on HookSource.
const (
	FederationHookSourceBuiltin = "builtin"
)

// Built-in federation providers. The constant set exists so call sites that
// switch on the value stay in sync as providers are added.
//
//   - gcp_iam   impersonates a per-user GCP service account using an admin SA
//     key (one credential per connection).
//   - gcp_oauth mints tokens from a per-user OAuth refresh token obtained via
//     Google's consent flow (zero service accounts; the identity in GCP audit
//     logs is the user's real Google account).
const (
	FederationProviderGCPIAM   = "gcp_iam"
	FederationProviderGCPOAuth = "gcp_oauth"
)

// Fallback policies for federation resolution failures. The semantics are
// enforced by the federation service: deny aborts the session; static skips
// federation for the session and leaves the connection's existing static
// credentials in place so it runs the pre-federation flow.
const (
	FederationFallbackDeny   = "deny"
	FederationFallbackStatic = "static"
)

// ConnectionFederationConfig holds per-connection identity-federation settings.
// One row per connection (unique key on connection_id).
//
// AdminCredentialsEncrypted is the AES-256-GCM ciphertext of the admin
// service-account JSON the built-in provider uses to mint per-user tokens. It
// is never returned to API callers in plaintext; the encrypted bytes are only
// decrypted at session-open time inside the federation service.
type ConnectionFederationConfig struct {
	ID                        string          `gorm:"column:id"`
	OrgID                     string          `gorm:"column:org_id"`
	ConnectionID              string          `gorm:"column:connection_id"`
	HookSource                string          `gorm:"column:hook_source"`
	BuiltinProvider           *string         `gorm:"column:builtin_provider"`
	AdminCredentialsEncrypted []byte          `gorm:"column:admin_credentials_encrypted"`
	IdentitySourceAttribute   string          `gorm:"column:identity_source_attribute"`
	IdentityTargetTemplate    string          `gorm:"column:identity_target_template"`
	FallbackPolicy            string          `gorm:"column:fallback_policy"`
	TokenTTLSeconds           int             `gorm:"column:token_ttl_seconds"`
	ExtraConfig               json.RawMessage `gorm:"column:extra_config"`
	CreatedAt                 time.Time       `gorm:"column:created_at"`
	UpdatedAt                 time.Time       `gorm:"column:updated_at"`
}

const connectionFederationConfigsTable = "private.connection_federation_configs"

// GetConnectionFederationConfig returns the federation config for a connection.
// Returns ErrNotFound when no federation is configured for the connection.
func GetConnectionFederationConfig(db *gorm.DB, orgID, connectionID string) (*ConnectionFederationConfig, error) {
	var resp ConnectionFederationConfig
	err := db.Table(connectionFederationConfigsTable).
		Where("org_id = ? AND connection_id = ?", orgID, connectionID).
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

// UpsertConnectionFederationConfig creates or updates the federation config for
// a connection. The unique constraint on connection_id collapses repeated
// configurations into a single row. UpdatedAt is refreshed on every write.
func UpsertConnectionFederationConfig(db *gorm.DB, cfg *ConnectionFederationConfig) error {
	cfg.UpdatedAt = time.Now().UTC()
	if cfg.CreatedAt.IsZero() {
		cfg.CreatedAt = cfg.UpdatedAt
	}
	if cfg.ExtraConfig == nil {
		cfg.ExtraConfig = json.RawMessage(`{}`)
	}
	return db.Exec(`
		INSERT INTO private.connection_federation_configs (
			id, org_id, connection_id, hook_source, builtin_provider,
			admin_credentials_encrypted,
			identity_source_attribute, identity_target_template,
			fallback_policy, token_ttl_seconds,
			extra_config, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (connection_id) DO UPDATE SET
			hook_source                 = EXCLUDED.hook_source,
			builtin_provider            = EXCLUDED.builtin_provider,
			admin_credentials_encrypted = EXCLUDED.admin_credentials_encrypted,
			identity_source_attribute   = EXCLUDED.identity_source_attribute,
			identity_target_template    = EXCLUDED.identity_target_template,
			fallback_policy             = EXCLUDED.fallback_policy,
			token_ttl_seconds           = EXCLUDED.token_ttl_seconds,
			extra_config                = EXCLUDED.extra_config,
			updated_at                  = EXCLUDED.updated_at
	`,
		cfg.ID, cfg.OrgID, cfg.ConnectionID, cfg.HookSource, cfg.BuiltinProvider,
		cfg.AdminCredentialsEncrypted,
		cfg.IdentitySourceAttribute, cfg.IdentityTargetTemplate,
		cfg.FallbackPolicy, cfg.TokenTTLSeconds,
		cfg.ExtraConfig, cfg.CreatedAt, cfg.UpdatedAt,
	).Error
}

// DeleteConnectionFederationConfig removes the federation config for a
// connection. Returns ErrNotFound when no row matched.
func DeleteConnectionFederationConfig(db *gorm.DB, orgID, connectionID string) error {
	res := db.Exec(`
		DELETE FROM private.connection_federation_configs
		WHERE org_id = ? AND connection_id = ?
	`, orgID, connectionID)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
