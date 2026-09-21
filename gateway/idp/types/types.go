package idptypes

import (
	"encoding/json"
	"errors"
)

// ErrDirectoryGroupsUnsupported is returned when the configured provider
// cannot list the directory groups with the configured client credentials.
var ErrDirectoryGroupsUnsupported = errors.New("directory group listing is not supported by this provider")

// DirectoryGroup is one group of the identity provider directory.
type DirectoryGroup struct {
	ID   string
	Name string
}

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
