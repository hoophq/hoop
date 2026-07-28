package oidcprovider

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAudienceContains(t *testing.T) {
	const resource = "https://demo.hoop.dev/api/mcp"

	t.Run("exact match", func(t *testing.T) {
		assert.True(t, audienceContains(resource, resource))
	})

	// Cosmetic variances between the IdP-minted audience and the configured
	// resource URI must not reject a semantically identical resource.
	t.Run("canonical uri variances match", func(t *testing.T) {
		for _, aud := range []string{
			"https://demo.hoop.dev/api/mcp/",
			"https://DEMO.hoop.dev/api/mcp",
			"https://demo.hoop.dev:443/api/mcp",
		} {
			assert.True(t, audienceContains(aud, resource), "aud=%s", aud)
			// Variance on the configured side instead of the token side.
			assert.True(t, audienceContains(resource, aud), "want=%s", aud)
		}
		assert.True(t, audienceContains("http://demo.hoop.dev:80/api/mcp", "http://demo.hoop.dev/api/mcp"))
	})

	t.Run("different resources rejected", func(t *testing.T) {
		for _, aud := range []string{
			"https://demo.hoop.dev/mcp",
			"https://other.hoop.dev/api/mcp",
			"http://demo.hoop.dev/api/mcp",
			"https://demo.hoop.dev:8443/api/mcp",
			"https://demo.hoop.dev/API/MCP", // paths are case-sensitive per RFC 3986
		} {
			assert.False(t, audienceContains(aud, resource), "aud=%s", aud)
		}
	})

	t.Run("array claim shapes", func(t *testing.T) {
		assert.True(t, audienceContains([]any{"other", resource + "/"}, resource))
		assert.True(t, audienceContains([]string{"other", resource}, resource))
		assert.False(t, audienceContains([]any{"other"}, resource))
		assert.False(t, audienceContains(nil, resource))
	})

	t.Run("opaque client ids stay case-sensitive exact", func(t *testing.T) {
		assert.True(t, audienceContains("hoop-MCP-Client", "hoop-MCP-Client"))
		assert.False(t, audienceContains("hoop-mcp-client", "hoop-MCP-Client"))
		assert.False(t, audienceContains("hoop-MCP-Client/", "hoop-MCP-Client"))
	})
}

func TestCanonicalResourceURI(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://demo.hoop.dev/api/mcp", "https://demo.hoop.dev/api/mcp"},
		{"https://DEMO.hoop.dev:443/api/mcp/", "https://demo.hoop.dev/api/mcp"},
		{"http://gw.internal:80/api/mcp", "http://gw.internal/api/mcp"},
		{"http://gw.internal:8009/api/mcp", "http://gw.internal:8009/api/mcp"},
		{"https://demo.hoop.dev", "https://demo.hoop.dev"},
		{"https://demo.hoop.dev/", "https://demo.hoop.dev"},
		// Non-URI audiences pass through untouched.
		{"hoop-mcp", "hoop-mcp"},
		{"urn:example:mcp", "urn:example:mcp"},
		{"", ""},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, canonicalResourceURI(tc.in), "in=%s", tc.in)
	}
}
