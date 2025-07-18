package models

import (
	"time"

	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

type ServerAuthConfig struct {
	OrgID                 string                `gorm:"column:org_id"`
	AuthMethod            *string               `gorm:"column:auth_method"`
	OidcConfig            *ServerAuthOidcConfig `gorm:"column:oidc_config;serializer:json"`
	SamlConfig            *ServerAuthSamlConfig `gorm:"column:saml_config;serializer:json"`
	ApiKey                *string               `gorm:"column:api_key"`
	RolloutApiKey         *string               `gorm:"column:rollout_api_key"`
	WebappUsersManagement *string               `gorm:"column:webapp_users_management"`
	AdminRoleName         *string               `gorm:"column:admin_role_name"`
	AuditorRoleName       *string               `gorm:"column:auditor_role_name"`
	ProductAnalytics      *string               `gorm:"column:product_analytics;->"`
	GrpcServerURL         *string               `gorm:"column:grpc_server_url;->"`
	SharedSigningKey      *string               `gorm:"column:shared_signing_key;->"`
	UpdatedAt             time.Time             `gorm:"column:updated_at"`
}

type ServerAuthOidcConfig struct {
	IssuerURL    string         `json:"issuer_url"`
	ClientID     string         `json:"client_id"`
	ClientSecret string         `json:"client_secret"`
	Scopes       pq.StringArray `json:"scopes"`
	Audience     string         `json:"audience"`
	GroupsClaim  string         `json:"groups_claim"`
}

type ServerAuthSamlConfig struct {
	IdpMetadataURL string `json:"idp_metadata_url"`
	GroupsClaim    string `json:"groups_claim"`
}

func GetServerAuthConfig() (*ServerAuthConfig, error) {
	var config ServerAuthConfig
	err := DB.Raw(`
	WITH authconfig AS (
		SELECT
			org_id, auth_method, oidc_config, saml_config, api_key, rollout_api_key,
			webapp_users_management, admin_role_name, auditor_role_name, updated_at
		FROM private.authconfig
	), serverconfig AS (
		SELECT product_analytics, grpc_server_url, shared_signing_key FROM private.serverconfig
	)
	SELECT * FROM authconfig
	FULL OUTER JOIN serverconfig ON true
	`).First(&config).
		Error
	if err == gorm.ErrRecordNotFound {
		return nil, ErrNotFound
	}
	return &config, err
}

// Create or update the server auth config.
// If the config already exists, it will be updated with the new values.
// The api_key attribute is optional, if not provided it will not be updated or created
//
// Updating roles performs a global update on the user_groups table to change the previous
// role names to the new ones.
func UpdateServerAuthConfig(newObj *ServerAuthConfig) (*ServerAuthConfig, error) {
	updatePayload := map[string]any{
		"auth_method":             newObj.AuthMethod,
		"oidc_config":             newObj.OidcConfig,
		"saml_config":             newObj.SamlConfig,
		"rollout_api_key":         newObj.RolloutApiKey,
		"webapp_users_management": newObj.WebappUsersManagement,
		"admin_role_name":         newObj.AdminRoleName,
		"auditor_role_name":       newObj.AuditorRoleName,
		"updated_at":              time.Now().UTC(),
	}
	if newObj.ApiKey != nil {
		updatePayload["api_key"] = newObj.ApiKey
	}

	return newObj, DB.Transaction(func(tx *gorm.DB) error {
		res := tx.Table("private.authconfig").
			Where("org_id = ?", newObj.OrgID).
			Updates(updatePayload)
		if res.Error != nil {
			return res.Error
		}

		if res.RowsAffected == 0 {
			err := tx.Table("private.authconfig").
				Create(newObj).
				Error
			if err != nil {
				return err
			}
		}

		// update all user groups with the new admin and auditor role names
		var err error
		if newObj.AdminRoleName != nil && *newObj.AdminRoleName != "" {
			err = tx.Session(&gorm.Session{AllowGlobalUpdate: true}).
				Table("private.user_groups").
				Where("org_id = ? AND name = ?", newObj.OrgID, types.GroupAdmin).
				Update("name", *newObj.AdminRoleName).
				Error
		}

		if newObj.AuditorRoleName != nil && *newObj.AuditorRoleName != "" {
			err = tx.Session(&gorm.Session{AllowGlobalUpdate: true}).
				Table("private.user_groups").
				Where("org_id = ? AND name = ?", newObj.OrgID, types.GroupAuditor).
				Update("name", *newObj.AuditorRoleName).
				Error
		}
		return err
	})
}
