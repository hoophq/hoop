package localprovider

import (
	"testing"

	"github.com/hoophq/hoop/gateway/models"
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
		{
			name: "grpc url unchanged",
			old: &models.ServerAuthConfig{
				GrpcServerURL: strPtr("grpc://localhost:9090"),
			},
			new: &models.ServerAuthConfig{
				GrpcServerURL: strPtr("grpc://localhost:9090"),
			},
			expected: false,
		},

		// SharedSigningKey changes
		{
			name: "shared signing key added",
			old:  &models.ServerAuthConfig{},
			new: &models.ServerAuthConfig{
				SharedSigningKey: strPtr("signing-key-123"),
			},
			expected: true,
		},
		{
			name: "shared signing key removed",
			old: &models.ServerAuthConfig{
				SharedSigningKey: strPtr("signing-key-123"),
			},
			new:      &models.ServerAuthConfig{},
			expected: true,
		},
		{
			name: "shared signing key changed",
			old: &models.ServerAuthConfig{
				SharedSigningKey: strPtr("signing-key-123"),
			},
			new: &models.ServerAuthConfig{
				SharedSigningKey: strPtr("signing-key-456"),
			},
			expected: true,
		},
		{
			name: "shared signing key unchanged",
			old: &models.ServerAuthConfig{
				SharedSigningKey: strPtr("signing-key-123"),
			},
			new: &models.ServerAuthConfig{
				SharedSigningKey: strPtr("signing-key-123"),
			},
			expected: false,
		},

		// Multiple field changes
		{
			name: "multiple fields changed",
			old: &models.ServerAuthConfig{
				AuthMethod:    strPtr("oauth"),
				ApiKey:        strPtr("old-key"),
				GrpcServerURL: strPtr("grpc://old-server:9090"),
			},
			new: &models.ServerAuthConfig{
				AuthMethod:    strPtr("saml"),
				ApiKey:        strPtr("new-key"),
				GrpcServerURL: strPtr("grpc://new-server:9091"),
			},
			expected: true,
		},
		{
			name: "some fields changed, some unchanged",
			old: &models.ServerAuthConfig{
				AuthMethod:       strPtr("oauth"),
				ApiKey:           strPtr("same-key"),
				GrpcServerURL:    strPtr("grpc://localhost:9090"),
				SharedSigningKey: strPtr("old-signing-key"),
			},
			new: &models.ServerAuthConfig{
				AuthMethod:       strPtr("saml"),                  // changed
				ApiKey:           strPtr("same-key"),              // unchanged
				GrpcServerURL:    strPtr("grpc://localhost:9090"), // unchanged
				SharedSigningKey: strPtr("new-signing-key"),       // changed
			},
			expected: true,
		},
		{
			name: "all tracked fields unchanged",
			old: &models.ServerAuthConfig{
				AuthMethod:       strPtr("oauth"),
				ApiKey:           strPtr("same-key"),
				GrpcServerURL:    strPtr("grpc://localhost:9090"),
				SharedSigningKey: strPtr("same-signing-key"),
			},
			new: &models.ServerAuthConfig{
				AuthMethod:       strPtr("oauth"),
				ApiKey:           strPtr("same-key"),
				GrpcServerURL:    strPtr("grpc://localhost:9090"),
				SharedSigningKey: strPtr("same-signing-key"),
			},
			expected: false,
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
			expected: false, // both resolve to empty string via toStr
		},
		{
			name: "empty string vs nil api key",
			old: &models.ServerAuthConfig{
				ApiKey: strPtr(""),
			},
			new: &models.ServerAuthConfig{
				ApiKey: nil,
			},
			expected: false,
		},
		{
			name: "empty string vs nil grpc url",
			old: &models.ServerAuthConfig{
				GrpcServerURL: strPtr(""),
			},
			new: &models.ServerAuthConfig{
				GrpcServerURL: nil,
			},
			expected: false,
		},
		{
			name: "empty string vs nil signing key",
			old: &models.ServerAuthConfig{
				SharedSigningKey: strPtr(""),
			},
			new: &models.ServerAuthConfig{
				SharedSigningKey: nil,
			},
			expected: false,
		},

		// Whitespace handling
		{
			name: "whitespace in auth method",
			old: &models.ServerAuthConfig{
				AuthMethod: strPtr("oauth"),
			},
			new: &models.ServerAuthConfig{
				AuthMethod: strPtr(" oauth "), // Note: toStr doesn't trim
			},
			expected: true,
		},
		{
			name: "whitespace in api key",
			old: &models.ServerAuthConfig{
				ApiKey: strPtr("key123"),
			},
			new: &models.ServerAuthConfig{
				ApiKey: strPtr("key123 "),
			},
			expected: true,
		},

		// All tracked fields set with different values
		{
			name: "all tracked fields different",
			old: &models.ServerAuthConfig{
				AuthMethod:       strPtr("oauth"),
				ApiKey:           strPtr("api-key-old"),
				GrpcServerURL:    strPtr("grpc://old-server:9090"),
				SharedSigningKey: strPtr("old-signing-key"),
			},
			new: &models.ServerAuthConfig{
				AuthMethod:       strPtr("saml"),
				ApiKey:           strPtr("api-key-new"),
				GrpcServerURL:    strPtr("grpc://new-server:9091"),
				SharedSigningKey: strPtr("new-signing-key"),
			},
			expected: true,
		},
		{
			name: "all tracked fields same",
			old: &models.ServerAuthConfig{
				AuthMethod:       strPtr("oauth"),
				ApiKey:           strPtr("api-key"),
				GrpcServerURL:    strPtr("grpc://server:9090"),
				SharedSigningKey: strPtr("signing-key"),
			},
			new: &models.ServerAuthConfig{
				AuthMethod:       strPtr("oauth"),
				ApiKey:           strPtr("api-key"),
				GrpcServerURL:    strPtr("grpc://server:9090"),
				SharedSigningKey: strPtr("signing-key"),
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hasServerConfigChanged(tt.old, tt.new)
			assert.Equal(t, tt.expected, result, "Test case: %s", tt.name)
		})
	}
}
