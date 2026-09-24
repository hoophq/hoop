package idptypes

import "encoding/json"

type ProviderType string

const (
	ProviderTypeOIDC  ProviderType = "oidc"
	ProviderTypeIDP   ProviderType = "idp" // Deprecated: Use ProviderTypeOIDC instead.
	ProviderTypeSAML  ProviderType = "saml"
	ProviderTypeLocal ProviderType = "local"
)

type ProviderUserInfo struct {
	Subject       string
	Email         string
	EmailVerified *bool
	Groups        []string
	Profile       string
	Picture       string

	MustSyncGroups       bool
	MustSyncGsuiteGroups bool
}

type ServerConfig struct {
	OrgID          string
	OrgLicenseData json.RawMessage
	AuthMethod     ProviderType
	ApiKey         string
	GrpcURL        string
}
