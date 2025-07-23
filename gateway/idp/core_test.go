package idp

import (
	"testing"

	idptypes "github.com/hoophq/hoop/gateway/idp/types"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/stretchr/testify/assert"
)

// Helper function to create string pointers
func strPtr(s string) *string {
	return &s
}

func TestUserInfoTokenVerifier_hasServerConfigChanged(t *testing.T) {

	t.Run("ProviderTypeLocal", func(t *testing.T) {
		verifier := userInfoTokenVerifier{}
		t.Run("both configs nil should return false", func(t *testing.T) {
			changed := verifier.hasServerConfigChanged(idptypes.ProviderTypeLocal, nil, nil)
			assert.False(t, changed)
		})

		t.Run("old nil, new has values should return true", func(t *testing.T) {
			newConfig := &models.ServerAuthConfig{
				AuthMethod:       strPtr("jwt"),
				ApiKey:           strPtr("key123"),
				GrpcServerURL:    strPtr("localhost:8080"),
				SharedSigningKey: strPtr("secret"),
			}
			changed := verifier.hasServerConfigChanged(idptypes.ProviderTypeLocal, nil, newConfig)
			assert.True(t, changed)
		})

		t.Run("old has values, new nil should return true", func(t *testing.T) {
			oldConfig := &models.ServerAuthConfig{
				AuthMethod:       strPtr("jwt"),
				ApiKey:           strPtr("key123"),
				GrpcServerURL:    strPtr("localhost:8080"),
				SharedSigningKey: strPtr("secret"),
			}
			changed := verifier.hasServerConfigChanged(idptypes.ProviderTypeLocal, oldConfig, nil)
			assert.True(t, changed)
		})

		t.Run("identical configs should return false", func(t *testing.T) {
			config := &models.ServerAuthConfig{
				AuthMethod:       strPtr("jwt"),
				ApiKey:           strPtr("key123"),
				GrpcServerURL:    strPtr("localhost:8080"),
				SharedSigningKey: strPtr("secret"),
			}
			changed := verifier.hasServerConfigChanged(idptypes.ProviderTypeLocal, config, config)
			assert.False(t, changed)
		})

		t.Run("different AuthMethod should return true", func(t *testing.T) {
			oldConfig := &models.ServerAuthConfig{
				AuthMethod:       strPtr("jwt"),
				ApiKey:           strPtr("key123"),
				GrpcServerURL:    strPtr("localhost:8080"),
				SharedSigningKey: strPtr("secret"),
			}
			newConfig := &models.ServerAuthConfig{
				AuthMethod:       strPtr("basic"),
				ApiKey:           strPtr("key123"),
				GrpcServerURL:    strPtr("localhost:8080"),
				SharedSigningKey: strPtr("secret"),
			}
			changed := verifier.hasServerConfigChanged(idptypes.ProviderTypeLocal, oldConfig, newConfig)
			assert.True(t, changed)
		})

		t.Run("different ApiKey should return true", func(t *testing.T) {
			oldConfig := &models.ServerAuthConfig{
				AuthMethod:       strPtr("jwt"),
				ApiKey:           strPtr("key123"),
				GrpcServerURL:    strPtr("localhost:8080"),
				SharedSigningKey: strPtr("secret"),
			}
			newConfig := &models.ServerAuthConfig{
				AuthMethod:       strPtr("jwt"),
				ApiKey:           strPtr("key456"),
				GrpcServerURL:    strPtr("localhost:8080"),
				SharedSigningKey: strPtr("secret"),
			}
			changed := verifier.hasServerConfigChanged(idptypes.ProviderTypeLocal, oldConfig, newConfig)
			assert.True(t, changed)
		})

		t.Run("nil to non-nil field should return true", func(t *testing.T) {
			oldConfig := &models.ServerAuthConfig{
				AuthMethod:       nil,
				ApiKey:           strPtr("key123"),
				GrpcServerURL:    strPtr("localhost:8080"),
				SharedSigningKey: strPtr("secret"),
			}
			newConfig := &models.ServerAuthConfig{
				AuthMethod:       strPtr("jwt"),
				ApiKey:           strPtr("key123"),
				GrpcServerURL:    strPtr("localhost:8080"),
				SharedSigningKey: strPtr("secret"),
			}
			changed := verifier.hasServerConfigChanged(idptypes.ProviderTypeLocal, oldConfig, newConfig)
			assert.True(t, changed)
		})
	})

	t.Run("ProviderTypeOIDC", func(t *testing.T) {
		verifier := userInfoTokenVerifier{}
		t.Run("both configs nil should return false", func(t *testing.T) {
			changed := verifier.hasServerConfigChanged(idptypes.ProviderTypeOIDC, nil, nil)
			assert.False(t, changed)
		})

		t.Run("identical OIDC configs should return false", func(t *testing.T) {
			oidcConfig := &models.ServerAuthOidcConfig{
				IssuerURL:    "https://issuer.example.com",
				ClientID:     "client123",
				ClientSecret: "secret456",
				Audience:     "audience789",
				Scopes:       []string{"my-custom-scope"},
				GroupsClaim:  "groups",
			}
			config := &models.ServerAuthConfig{
				AuthMethod:    strPtr("oidc"),
				ApiKey:        strPtr("key123"),
				GrpcServerURL: strPtr("localhost:8080"),
				OidcConfig:    oidcConfig,
			}
			changed := verifier.hasServerConfigChanged(idptypes.ProviderTypeOIDC, config, config)
			assert.False(t, changed)
		})

		t.Run("different OIDC IssuerURL should return true", func(t *testing.T) {
			oldOidcConfig := &models.ServerAuthOidcConfig{
				IssuerURL:    "https://old-issuer.example.com",
				ClientID:     "client123",
				ClientSecret: "secret456",
				Audience:     "audience789",
				Scopes:       []string{"my-custom-scope"},
				GroupsClaim:  "groups",
			}
			newOidcConfig := &models.ServerAuthOidcConfig{
				IssuerURL:    "https://new-issuer.example.com",
				ClientID:     "client123",
				ClientSecret: "secret456",
				Audience:     "audience789",
				Scopes:       []string{"my-custom-scope"},
				GroupsClaim:  "groups",
			}
			oldConfig := &models.ServerAuthConfig{
				AuthMethod:    strPtr("oidc"),
				ApiKey:        strPtr("key123"),
				GrpcServerURL: strPtr("localhost:8080"),
				OidcConfig:    oldOidcConfig,
			}
			newConfig := &models.ServerAuthConfig{
				AuthMethod:    strPtr("oidc"),
				ApiKey:        strPtr("key123"),
				GrpcServerURL: strPtr("localhost:8080"),
				OidcConfig:    newOidcConfig,
			}
			changed := verifier.hasServerConfigChanged(idptypes.ProviderTypeOIDC, oldConfig, newConfig)
			assert.True(t, changed)
		})

		t.Run("nil OIDC config to non-nil should return true", func(t *testing.T) {
			oldConfig := &models.ServerAuthConfig{
				AuthMethod:    strPtr("oidc"),
				ApiKey:        strPtr("key123"),
				GrpcServerURL: strPtr("localhost:8080"),
				OidcConfig:    nil,
			}
			newConfig := &models.ServerAuthConfig{
				AuthMethod:    strPtr("oidc"),
				ApiKey:        strPtr("key123"),
				GrpcServerURL: strPtr("localhost:8080"),
				OidcConfig: &models.ServerAuthOidcConfig{
					IssuerURL: "https://issuer.example.com",
				},
			}
			changed := verifier.hasServerConfigChanged(idptypes.ProviderTypeOIDC, oldConfig, newConfig)
			assert.True(t, changed)
		})

		t.Run("different ClientSecret should return true", func(t *testing.T) {
			oldOidcConfig := &models.ServerAuthOidcConfig{
				IssuerURL:    "https://issuer.example.com",
				ClientID:     "client123",
				ClientSecret: "old-secret",
				Audience:     "audience789",
				Scopes:       []string{"my-custom-scope"},
				GroupsClaim:  "groups",
			}
			newOidcConfig := &models.ServerAuthOidcConfig{
				IssuerURL:    "https://issuer.example.com",
				ClientID:     "client123",
				ClientSecret: "new-secret",
				Audience:     "audience789",
				Scopes:       []string{"my-custom-scope"},
				GroupsClaim:  "groups",
			}
			oldConfig := &models.ServerAuthConfig{
				AuthMethod:    strPtr("oidc"),
				ApiKey:        strPtr("key123"),
				GrpcServerURL: strPtr("localhost:8080"),
				OidcConfig:    oldOidcConfig,
			}
			newConfig := &models.ServerAuthConfig{
				AuthMethod:    strPtr("oidc"),
				ApiKey:        strPtr("key123"),
				GrpcServerURL: strPtr("localhost:8080"),
				OidcConfig:    newOidcConfig,
			}
			changed := verifier.hasServerConfigChanged(idptypes.ProviderTypeOIDC, oldConfig, newConfig)
			assert.True(t, changed)
		})
	})

	t.Run("ProviderTypeOIDC", func(t *testing.T) {
		verifier := userInfoTokenVerifier{}
		t.Run("should behave same as OIDC", func(t *testing.T) {
			oidcConfig := &models.ServerAuthOidcConfig{
				IssuerURL:    "https://issuer.example.com",
				ClientID:     "client123",
				ClientSecret: "secret456",
				Audience:     "audience789",
				Scopes:       []string{"my-custom-scope"},
				GroupsClaim:  "groups",
			}
			config := &models.ServerAuthConfig{
				AuthMethod:    strPtr("oidc"),
				ApiKey:        strPtr("key123"),
				GrpcServerURL: strPtr("localhost:8080"),
				OidcConfig:    oidcConfig,
			}
			changed := verifier.hasServerConfigChanged(idptypes.ProviderTypeOIDC, config, config)
			assert.False(t, changed)
		})
	})

	t.Run("ProviderTypeSAML", func(t *testing.T) {
		verifier := userInfoTokenVerifier{}
		t.Run("both configs nil should return false", func(t *testing.T) {
			changed := verifier.hasServerConfigChanged(idptypes.ProviderTypeSAML, nil, nil)
			assert.False(t, changed)
		})

		t.Run("identical SAML configs should return false", func(t *testing.T) {
			samlConfig := &models.ServerAuthSamlConfig{
				IdpMetadataURL: "https://saml.example.com/metadata",
				GroupsClaim:    "groups",
			}
			config := &models.ServerAuthConfig{
				AuthMethod:       strPtr("saml"),
				ApiKey:           strPtr("key123"),
				GrpcServerURL:    strPtr("localhost:8080"),
				SharedSigningKey: strPtr("saml-secret"),
				SamlConfig:       samlConfig,
			}
			changed := verifier.hasServerConfigChanged(idptypes.ProviderTypeSAML, config, config)
			assert.False(t, changed)
		})

		t.Run("different SAML IdpMetadataURL should return true", func(t *testing.T) {
			oldSamlConfig := &models.ServerAuthSamlConfig{
				IdpMetadataURL: "https://old-saml.example.com/metadata",
				GroupsClaim:    "groups",
			}
			newSamlConfig := &models.ServerAuthSamlConfig{
				IdpMetadataURL: "https://new-saml.example.com/metadata",
				GroupsClaim:    "groups",
			}
			oldConfig := &models.ServerAuthConfig{
				AuthMethod:       strPtr("saml"),
				ApiKey:           strPtr("key123"),
				GrpcServerURL:    strPtr("localhost:8080"),
				SharedSigningKey: strPtr("saml-secret"),
				SamlConfig:       oldSamlConfig,
			}
			newConfig := &models.ServerAuthConfig{
				AuthMethod:       strPtr("saml"),
				ApiKey:           strPtr("key123"),
				GrpcServerURL:    strPtr("localhost:8080"),
				SharedSigningKey: strPtr("saml-secret"),
				SamlConfig:       newSamlConfig,
			}
			changed := verifier.hasServerConfigChanged(idptypes.ProviderTypeSAML, oldConfig, newConfig)
			assert.True(t, changed)
		})

		t.Run("nil SAML config to non-nil should return true", func(t *testing.T) {
			oldConfig := &models.ServerAuthConfig{
				AuthMethod:       strPtr("saml"),
				ApiKey:           strPtr("key123"),
				GrpcServerURL:    strPtr("localhost:8080"),
				SharedSigningKey: strPtr("saml-secret"),
				SamlConfig:       nil,
			}
			newConfig := &models.ServerAuthConfig{
				AuthMethod:       strPtr("saml"),
				ApiKey:           strPtr("key123"),
				GrpcServerURL:    strPtr("localhost:8080"),
				SharedSigningKey: strPtr("saml-secret"),
				SamlConfig: &models.ServerAuthSamlConfig{
					IdpMetadataURL: "https://saml.example.com/metadata",
				},
			}
			changed := verifier.hasServerConfigChanged(idptypes.ProviderTypeSAML, oldConfig, newConfig)
			assert.True(t, changed)
		})

		t.Run("different GroupsClaim should return true", func(t *testing.T) {
			oldSamlConfig := &models.ServerAuthSamlConfig{
				IdpMetadataURL: "https://saml.example.com/metadata",
				GroupsClaim:    "old-groups",
			}
			newSamlConfig := &models.ServerAuthSamlConfig{
				IdpMetadataURL: "https://saml.example.com/metadata",
				GroupsClaim:    "new-groups",
			}
			oldConfig := &models.ServerAuthConfig{
				AuthMethod:       strPtr("saml"),
				ApiKey:           strPtr("key123"),
				GrpcServerURL:    strPtr("localhost:8080"),
				SharedSigningKey: strPtr("saml-secret"),
				SamlConfig:       oldSamlConfig,
			}
			newConfig := &models.ServerAuthConfig{
				AuthMethod:       strPtr("saml"),
				ApiKey:           strPtr("key123"),
				GrpcServerURL:    strPtr("localhost:8080"),
				SharedSigningKey: strPtr("saml-secret"),
				SamlConfig:       newSamlConfig,
			}
			changed := verifier.hasServerConfigChanged(idptypes.ProviderTypeSAML, oldConfig, newConfig)
			assert.True(t, changed)
		})
	})

	t.Run("Unknown provider type", func(t *testing.T) {
		verifier := userInfoTokenVerifier{}
		t.Run("should return true and log warning", func(t *testing.T) {
			// Assuming there's an unknown provider type constant or we can cast a string
			unknownProviderType := idptypes.ProviderType("unknown")
			changed := verifier.hasServerConfigChanged(unknownProviderType, nil, nil)
			assert.True(t, changed)
		})
	})
}
