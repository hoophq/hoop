package samlprovider

import (
	"testing"
	"time"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/stretchr/testify/assert"
)

// Helper function to create string pointers
func strPtr(s string) *string { return &s }

func TestHasServerConfigChanges(t *testing.T) {
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
				AuthMethod: strPtr("oauth"),
			},
			expected: true,
		},
		{
			name: "auth method removed",
			old: &models.ServerAuthConfig{
				AuthMethod: strPtr("oauth"),
			},
			new:      &models.ServerAuthConfig{},
			expected: true,
		},
		{
			name: "auth method changed",
			old: &models.ServerAuthConfig{
				AuthMethod: strPtr("oauth"),
			},
			new: &models.ServerAuthConfig{
				AuthMethod: strPtr("saml"),
			},
			expected: true,
		},
		{
			name: "auth method unchanged",
			old: &models.ServerAuthConfig{
				AuthMethod: strPtr("oauth"),
			},
			new: &models.ServerAuthConfig{
				AuthMethod: strPtr("oauth"),
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

		// SharedSigningKey changes
		{
			name: "signing key added",
			old:  &models.ServerAuthConfig{},
			new: &models.ServerAuthConfig{
				SharedSigningKey: strPtr("signing-key-123"),
			},
			expected: true,
		},
		{
			name: "signing key removed",
			old: &models.ServerAuthConfig{
				SharedSigningKey: strPtr("signing-key-123"),
			},
			new:      &models.ServerAuthConfig{},
			expected: true,
		},
		{
			name: "signing key changed",
			old: &models.ServerAuthConfig{
				SharedSigningKey: strPtr("signing-key-123"),
			},
			new: &models.ServerAuthConfig{
				SharedSigningKey: strPtr("signing-key-456"),
			},
			expected: true,
		},

		// SAML Config changes
		{
			name: "saml config added",
			old:  &models.ServerAuthConfig{},
			new: &models.ServerAuthConfig{
				SamlConfig: &models.ServerAuthSamlConfig{
					IdpMetadataURL: "https://idp.example.com",
					GroupsClaim:    "groups",
				},
			},
			expected: true,
		},
		{
			name: "saml config removed",
			old: &models.ServerAuthConfig{
				SamlConfig: &models.ServerAuthSamlConfig{
					IdpMetadataURL: "https://idp.example.com",
					GroupsClaim:    "groups",
				},
			},
			new:      &models.ServerAuthConfig{},
			expected: true,
		},
		{
			name: "saml idp metadata url changed",
			old: &models.ServerAuthConfig{
				SamlConfig: &models.ServerAuthSamlConfig{
					IdpMetadataURL: "https://idp1.example.com",
					GroupsClaim:    "groups",
				},
			},
			new: &models.ServerAuthConfig{
				SamlConfig: &models.ServerAuthSamlConfig{
					IdpMetadataURL: "https://idp2.example.com",
					GroupsClaim:    "groups",
				},
			},
			expected: true,
		},
		{
			name: "saml groups claim changed",
			old: &models.ServerAuthConfig{
				SamlConfig: &models.ServerAuthSamlConfig{
					IdpMetadataURL: "https://idp.example.com",
					GroupsClaim:    "groups",
				},
			},
			new: &models.ServerAuthConfig{
				SamlConfig: &models.ServerAuthSamlConfig{
					IdpMetadataURL: "https://idp.example.com",
					GroupsClaim:    "roles",
				},
			},
			expected: true,
		},
		{
			name: "saml config unchanged",
			old: &models.ServerAuthConfig{
				SamlConfig: &models.ServerAuthSamlConfig{
					IdpMetadataURL: "https://idp.example.com",
					GroupsClaim:    "groups",
				},
			},
			new: &models.ServerAuthConfig{
				SamlConfig: &models.ServerAuthSamlConfig{
					IdpMetadataURL: "https://idp.example.com",
					GroupsClaim:    "groups",
				},
			},
			expected: false,
		},
		{
			name: "saml config nil to empty",
			old: &models.ServerAuthConfig{
				SamlConfig: nil,
			},
			new: &models.ServerAuthConfig{
				SamlConfig: &models.ServerAuthSamlConfig{
					IdpMetadataURL: "",
					GroupsClaim:    "",
				},
			},
			expected: false,
		},

		// Complex scenarios with multiple changes
		{
			name: "multiple fields changed",
			old: &models.ServerAuthConfig{
				AuthMethod: strPtr("oauth"),
				ApiKey:     strPtr("old-key"),
				SamlConfig: &models.ServerAuthSamlConfig{
					IdpMetadataURL: "https://old-idp.example.com",
					GroupsClaim:    "groups",
				},
			},
			new: &models.ServerAuthConfig{
				AuthMethod: strPtr("saml"),
				ApiKey:     strPtr("new-key"),
				SamlConfig: &models.ServerAuthSamlConfig{
					IdpMetadataURL: "https://new-idp.example.com",
					GroupsClaim:    "roles",
				},
			},
			expected: true,
		},
		{
			name: "some fields changed, some unchanged",
			old: &models.ServerAuthConfig{
				AuthMethod:    strPtr("oauth"),
				ApiKey:        strPtr("same-key"),
				GrpcServerURL: strPtr("grpc://localhost:9090"),
			},
			new: &models.ServerAuthConfig{
				AuthMethod:    strPtr("saml"),                  // changed
				ApiKey:        strPtr("same-key"),              // unchanged
				GrpcServerURL: strPtr("grpc://localhost:9090"), // unchanged
			},
			expected: true,
		},

		// Edge cases with empty strings vs nil
		{
			name: "empty string vs nil auth method",
			old: &models.ServerAuthConfig{
				AuthMethod: strPtr(""),
			},
			new: &models.ServerAuthConfig{
				AuthMethod: nil,
			},
			expected: false, // both resolve to empty string
		},
		{
			name: "whitespace in strings",
			old: &models.ServerAuthConfig{
				AuthMethod: strPtr("oauth"),
			},
			new: &models.ServerAuthConfig{
				AuthMethod: strPtr(" oauth "), // Note: toStr doesn't trim, so this should be different
			},
			expected: true,
		},

		// Fields not tracked by the function (should not cause changes)
		{
			name: "non-tracked fields changed",
			old: &models.ServerAuthConfig{
				OrgID:                 "org1",
				RolloutApiKey:         strPtr("rollout-key-1"),
				WebappUsersManagement: strPtr("manual"),
				AdminRoleName:         strPtr("admin"),
				AuditorRoleName:       strPtr("auditor"),
				ProductAnalytics:      strPtr("enabled"),
				UpdatedAt:             time.Now(),
			},
			new: &models.ServerAuthConfig{
				OrgID:                 "org2",                    // changed
				RolloutApiKey:         strPtr("rollout-key-2"),   // changed
				WebappUsersManagement: strPtr("auto"),            // changed
				AdminRoleName:         strPtr("administrator"),   // changed
				AuditorRoleName:       strPtr("viewer"),          // changed
				ProductAnalytics:      strPtr("disabled"),        // changed
				UpdatedAt:             time.Now().Add(time.Hour), // changed
			},
			expected: false, // these fields are not tracked by hasServerConfigChanges
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hasServerConfigChanged(tt.old, tt.new)
			assert.Equal(t, tt.expected, result, "Test case: %s", tt.name)
		})
	}
}
