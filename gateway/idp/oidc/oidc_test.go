package oidcprovider

import (
	"testing"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
)

// Helper function to create string pointers
func strPtr(s string) *string { return &s }

func TestHasServerConfigChanged(t *testing.T) {
	tests := []struct {
		name     string
		old      *models.ServerAuthConfig
		new      *models.ServerAuthConfig
		expected bool
	}{
		// Basic nil tests
		{
			name:     "both configs nil",
			old:      nil,
			new:      nil,
			expected: false,
		},
		{
			name:     "old nil, new empty",
			old:      nil,
			new:      &models.ServerAuthConfig{},
			expected: false,
		},
		{
			name:     "old empty, new nil",
			old:      &models.ServerAuthConfig{},
			new:      nil,
			expected: false,
		},
		{
			name:     "both configs empty",
			old:      &models.ServerAuthConfig{},
			new:      &models.ServerAuthConfig{},
			expected: false,
		},

		// AuthMethod changes
		{
			name: "auth method added",
			old:  &models.ServerAuthConfig{},
			new: &models.ServerAuthConfig{
				AuthMethod: strPtr("oidc"),
			},
			expected: true,
		},
		{
			name: "auth method removed",
			old: &models.ServerAuthConfig{
				AuthMethod: strPtr("oidc"),
			},
			new:      &models.ServerAuthConfig{},
			expected: true,
		},
		{
			name: "auth method changed",
			old: &models.ServerAuthConfig{
				AuthMethod: strPtr("oidc"),
			},
			new: &models.ServerAuthConfig{
				AuthMethod: strPtr("saml"),
			},
			expected: true,
		},
		{
			name: "auth method unchanged",
			old: &models.ServerAuthConfig{
				AuthMethod: strPtr("oidc"),
			},
			new: &models.ServerAuthConfig{
				AuthMethod: strPtr("oidc"),
			},
			expected: false,
		},

		// ApiKey changes
		{
			name: "api key added",
			old:  &models.ServerAuthConfig{},
			new: &models.ServerAuthConfig{
				ApiKey: strPtr("key123"),
			},
			expected: true,
		},
		{
			name: "api key removed",
			old: &models.ServerAuthConfig{
				ApiKey: strPtr("key123"),
			},
			new:      &models.ServerAuthConfig{},
			expected: true,
		},
		{
			name: "api key changed",
			old: &models.ServerAuthConfig{
				ApiKey: strPtr("key123"),
			},
			new: &models.ServerAuthConfig{
				ApiKey: strPtr("key456"),
			},
			expected: true,
		},
		{
			name: "api key unchanged",
			old: &models.ServerAuthConfig{
				ApiKey: strPtr("key123"),
			},
			new: &models.ServerAuthConfig{
				ApiKey: strPtr("key123"),
			},
			expected: false,
		},

		// GrpcServerURL changes
		{
			name: "grpc url added",
			old:  &models.ServerAuthConfig{},
			new: &models.ServerAuthConfig{
				GrpcServerURL: strPtr("grpc://localhost:9090"),
			},
			expected: true,
		},
		{
			name: "grpc url removed",
			old: &models.ServerAuthConfig{
				GrpcServerURL: strPtr("grpc://localhost:9090"),
			},
			new:      &models.ServerAuthConfig{},
			expected: true,
		},
		{
			name: "grpc url changed",
			old: &models.ServerAuthConfig{
				GrpcServerURL: strPtr("grpc://localhost:9090"),
			},
			new: &models.ServerAuthConfig{
				GrpcServerURL: strPtr("grpc://localhost:9091"),
			},
			expected: true,
		},

		// OIDC Config - IssuerURL changes
		{
			name: "oidc issuer url added",
			old:  &models.ServerAuthConfig{},
			new: &models.ServerAuthConfig{
				OidcConfig: &models.ServerAuthOidcConfig{
					IssuerURL: "https://auth.example.com",
				},
			},
			expected: true,
		},
		{
			name: "oidc issuer url removed",
			old: &models.ServerAuthConfig{
				OidcConfig: &models.ServerAuthOidcConfig{
					IssuerURL: "https://auth.example.com",
				},
			},
			new:      &models.ServerAuthConfig{},
			expected: true,
		},
		{
			name: "oidc issuer url changed",
			old: &models.ServerAuthConfig{
				OidcConfig: &models.ServerAuthOidcConfig{
					IssuerURL: "https://auth1.example.com",
				},
			},
			new: &models.ServerAuthConfig{
				OidcConfig: &models.ServerAuthOidcConfig{
					IssuerURL: "https://auth2.example.com",
				},
			},
			expected: true,
		},

		// OIDC Config - ClientID changes
		{
			name: "oidc client id added",
			old:  &models.ServerAuthConfig{},
			new: &models.ServerAuthConfig{
				OidcConfig: &models.ServerAuthOidcConfig{
					ClientID: "client123",
				},
			},
			expected: true,
		},
		{
			name: "oidc client id changed",
			old: &models.ServerAuthConfig{
				OidcConfig: &models.ServerAuthOidcConfig{
					ClientID: "client123",
				},
			},
			new: &models.ServerAuthConfig{
				OidcConfig: &models.ServerAuthOidcConfig{
					ClientID: "client456",
				},
			},
			expected: true,
		},

		// OIDC Config - ClientSecret changes
		{
			name: "oidc client secret added",
			old:  &models.ServerAuthConfig{},
			new: &models.ServerAuthConfig{
				OidcConfig: &models.ServerAuthOidcConfig{
					ClientSecret: "secret123",
				},
			},
			expected: true,
		},
		{
			name: "oidc client secret changed",
			old: &models.ServerAuthConfig{
				OidcConfig: &models.ServerAuthOidcConfig{
					ClientSecret: "secret123",
				},
			},
			new: &models.ServerAuthConfig{
				OidcConfig: &models.ServerAuthOidcConfig{
					ClientSecret: "secret456",
				},
			},
			expected: true,
		},

		// OIDC Config - Audience changes
		{
			name: "oidc audience added",
			old:  &models.ServerAuthConfig{},
			new: &models.ServerAuthConfig{
				OidcConfig: &models.ServerAuthOidcConfig{
					Audience: "myapp",
				},
			},
			expected: true,
		},
		{
			name: "oidc audience changed",
			old: &models.ServerAuthConfig{
				OidcConfig: &models.ServerAuthOidcConfig{
					Audience: "myapp",
				},
			},
			new: &models.ServerAuthConfig{
				OidcConfig: &models.ServerAuthOidcConfig{
					Audience: "yourapp",
				},
			},
			expected: true,
		},

		// OIDC Config - Scopes changes
		{
			name: "oidc scopes added",
			old:  &models.ServerAuthConfig{},
			new: &models.ServerAuthConfig{
				OidcConfig: &models.ServerAuthOidcConfig{
					Scopes: pq.StringArray{"openid", "profile"},
				},
			},
			expected: true,
		},
		{
			name: "oidc scopes changed",
			old: &models.ServerAuthConfig{
				OidcConfig: &models.ServerAuthOidcConfig{
					Scopes: pq.StringArray{"openid", "profile"},
				},
			},
			new: &models.ServerAuthConfig{
				OidcConfig: &models.ServerAuthOidcConfig{
					Scopes: pq.StringArray{"openid", "email"},
				},
			},
			expected: true,
		},
		{
			name: "oidc scopes order changed",
			old: &models.ServerAuthConfig{
				OidcConfig: &models.ServerAuthOidcConfig{
					Scopes: pq.StringArray{"profile", "openid"},
				},
			},
			new: &models.ServerAuthConfig{
				OidcConfig: &models.ServerAuthOidcConfig{
					Scopes: pq.StringArray{"openid", "profile"},
				},
			},
			expected: true, // Different order = different string representation
		},
		{
			name: "oidc scopes unchanged",
			old: &models.ServerAuthConfig{
				OidcConfig: &models.ServerAuthOidcConfig{
					Scopes: pq.StringArray{"openid", "profile"},
				},
			},
			new: &models.ServerAuthConfig{
				OidcConfig: &models.ServerAuthOidcConfig{
					Scopes: pq.StringArray{"openid", "profile"},
				},
			},
			expected: false,
		},

		// OIDC Config - GroupsClaim changes
		{
			name: "oidc groups claim added",
			old:  &models.ServerAuthConfig{},
			new: &models.ServerAuthConfig{
				OidcConfig: &models.ServerAuthOidcConfig{
					GroupsClaim: "groups",
				},
			},
			expected: true,
		},
		{
			name: "oidc groups claim changed",
			old: &models.ServerAuthConfig{
				OidcConfig: &models.ServerAuthOidcConfig{
					GroupsClaim: "groups",
				},
			},
			new: &models.ServerAuthConfig{
				OidcConfig: &models.ServerAuthOidcConfig{
					GroupsClaim: "roles",
				},
			},
			expected: true,
		},

		// Complete OIDC Config tests
		{
			name: "complete oidc config added",
			old:  &models.ServerAuthConfig{},
			new: &models.ServerAuthConfig{
				OidcConfig: &models.ServerAuthOidcConfig{
					IssuerURL:    "https://auth.example.com",
					ClientID:     "client123",
					ClientSecret: "secret123",
					Scopes:       pq.StringArray{"openid", "profile", "email"},
					Audience:     "myapp",
					GroupsClaim:  "groups",
				},
			},
			expected: true,
		},
		{
			name: "complete oidc config removed",
			old: &models.ServerAuthConfig{
				OidcConfig: &models.ServerAuthOidcConfig{
					IssuerURL:    "https://auth.example.com",
					ClientID:     "client123",
					ClientSecret: "secret123",
					Scopes:       pq.StringArray{"openid", "profile", "email"},
					Audience:     "myapp",
					GroupsClaim:  "groups",
				},
			},
			new:      &models.ServerAuthConfig{},
			expected: true,
		},
		{
			name: "complete oidc config unchanged",
			old: &models.ServerAuthConfig{
				OidcConfig: &models.ServerAuthOidcConfig{
					IssuerURL:    "https://auth.example.com",
					ClientID:     "client123",
					ClientSecret: "secret123",
					Scopes:       pq.StringArray{"openid", "profile"},
					Audience:     "myapp",
					GroupsClaim:  "groups",
				},
			},
			new: &models.ServerAuthConfig{
				OidcConfig: &models.ServerAuthOidcConfig{
					IssuerURL:    "https://auth.example.com",
					ClientID:     "client123",
					ClientSecret: "secret123",
					Scopes:       pq.StringArray{"openid", "profile"},
					Audience:     "myapp",
					GroupsClaim:  "groups",
				},
			},
			expected: false,
		},

		// Mixed OIDC and other field changes
		{
			name: "auth method and oidc config both changed",
			old: &models.ServerAuthConfig{
				AuthMethod: strPtr("saml"),
				OidcConfig: &models.ServerAuthOidcConfig{
					IssuerURL: "https://old-auth.example.com",
					ClientID:  "old-client",
				},
			},
			new: &models.ServerAuthConfig{
				AuthMethod: strPtr("oidc"),
				OidcConfig: &models.ServerAuthOidcConfig{
					IssuerURL: "https://new-auth.example.com",
					ClientID:  "new-client",
				},
			},
			expected: true,
		},
		{
			name: "api key changed, oidc unchanged",
			old: &models.ServerAuthConfig{
				ApiKey: strPtr("old-key"),
				OidcConfig: &models.ServerAuthOidcConfig{
					IssuerURL: "https://auth.example.com",
					ClientID:  "client123",
				},
			},
			new: &models.ServerAuthConfig{
				ApiKey: strPtr("new-key"),
				OidcConfig: &models.ServerAuthOidcConfig{
					IssuerURL: "https://auth.example.com",
					ClientID:  "client123",
				},
			},
			expected: true,
		},

		// Edge cases
		{
			name: "oidc config nil to empty struct",
			old: &models.ServerAuthConfig{
				OidcConfig: nil,
			},
			new: &models.ServerAuthConfig{
				OidcConfig: &models.ServerAuthOidcConfig{}, // all fields empty
			},
			expected: false, // empty values should be equivalent
		},
		{
			name: "empty string vs nil pointer fields",
			old: &models.ServerAuthConfig{
				AuthMethod: strPtr(""),
			},
			new: &models.ServerAuthConfig{
				AuthMethod: nil,
			},
			expected: false, // both resolve to empty string via toStr
		},
		{
			name: "empty scopes vs nil scopes",
			old: &models.ServerAuthConfig{
				OidcConfig: &models.ServerAuthOidcConfig{
					Scopes: pq.StringArray{},
				},
			},
			new: &models.ServerAuthConfig{
				OidcConfig: &models.ServerAuthOidcConfig{
					Scopes: nil,
				},
			},
			expected: false, // both should result in same string representation
		},

		// Complex scenario with all tracked fields
		{
			name: "all tracked fields changed",
			old: &models.ServerAuthConfig{
				AuthMethod:    strPtr("saml"),
				ApiKey:        strPtr("old-api-key"),
				GrpcServerURL: strPtr("grpc://old-server:9090"),
				OidcConfig: &models.ServerAuthOidcConfig{
					IssuerURL:    "https://old-auth.example.com",
					ClientID:     "old-client-id",
					ClientSecret: "old-client-secret",
					Scopes:       pq.StringArray{"openid"},
					Audience:     "old-audience",
					GroupsClaim:  "groups",
				},
			},
			new: &models.ServerAuthConfig{
				AuthMethod:    strPtr("oidc"),
				ApiKey:        strPtr("new-api-key"),
				GrpcServerURL: strPtr("grpc://new-server:9091"),
				OidcConfig: &models.ServerAuthOidcConfig{
					IssuerURL:    "https://new-auth.example.com",
					ClientID:     "new-client-id",
					ClientSecret: "new-client-secret",
					Scopes:       pq.StringArray{"openid", "profile", "email"},
					Audience:     "new-audience",
					GroupsClaim:  "roles",
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hasServerConfigChanged(tt.old, tt.new)
			assert.Equal(t, tt.expected, result, "Test case: %s", tt.name)
		})
	}
}
