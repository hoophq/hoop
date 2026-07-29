package oidcprovider

import (
	"testing"

	"github.com/golang-jwt/jwt/v5"
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
		// Query components are preserved: resources distinguished by query
		// parameters must not compare equal.
		{"https://demo.hoop.dev/api/mcp?tenant=a", "https://demo.hoop.dev/api/mcp?tenant=a"},
		{"https://DEMO.hoop.dev:443/api/mcp/?tenant=a", "https://demo.hoop.dev/api/mcp?tenant=a"},
		// Non-URI audiences pass through untouched.
		{"hoop-mcp", "hoop-mcp"},
		{"urn:example:mcp", "urn:example:mcp"},
		{"", ""},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, canonicalResourceURI(tc.in), "in=%s", tc.in)
	}
}

func TestAudienceQueryIsolation(t *testing.T) {
	assert.False(t, audienceContains("https://demo.hoop.dev/api/mcp?tenant=a", "https://demo.hoop.dev/api/mcp?tenant=b"))
	assert.False(t, audienceContains("https://demo.hoop.dev/api/mcp?tenant=a", "https://demo.hoop.dev/api/mcp"))
	assert.True(t, audienceContains("https://DEMO.hoop.dev/api/mcp?tenant=a", "https://demo.hoop.dev/api/mcp?tenant=a"))
}

func TestTokenBoundToClient(t *testing.T) {
	const clientID = "hoop-mcp-client"

	t.Run("client id in aud", func(t *testing.T) {
		assert.True(t, tokenBoundToClient(jwt.MapClaims{"aud": clientID}, []string{clientID}))
		assert.True(t, tokenBoundToClient(jwt.MapClaims{"aud": []any{"other", clientID}}, []string{clientID}))
	})

	// Auth0-style: aud carries the IdP's own default audience while the
	// requesting client surfaces only as the azp authorized party.
	t.Run("client id in azp with foreign aud", func(t *testing.T) {
		claims := jwt.MapClaims{
			"aud": []any{"https://tenant.us.auth0.com/userinfo"},
			"azp": clientID,
		}
		assert.True(t, tokenBoundToClient(claims, []string{clientID}))
	})

	t.Run("unrelated client rejected", func(t *testing.T) {
		claims := jwt.MapClaims{"aud": "something-else", "azp": "web-app-client"}
		assert.False(t, tokenBoundToClient(claims, []string{clientID}))
	})

	t.Run("azp is case-sensitive exact", func(t *testing.T) {
		assert.False(t, tokenBoundToClient(jwt.MapClaims{"azp": "HOOP-mcp-client"}, []string{clientID}))
	})

	// Client IDs are opaque identifiers (RFC 6749 §2.2) even when URL-shaped:
	// no canonicalization may conflate distinct clients.
	t.Run("url-shaped client ids compare byte-exactly", func(t *testing.T) {
		const urlClientID = "https://apps.example.com/mcp-client"
		assert.True(t, tokenBoundToClient(jwt.MapClaims{"aud": urlClientID}, []string{urlClientID}))
		for _, aud := range []string{
			urlClientID + "/",
			"https://APPS.example.com/mcp-client",
			"https://apps.example.com:443/mcp-client",
		} {
			assert.False(t, tokenBoundToClient(jwt.MapClaims{"aud": aud}, []string{urlClientID}), "aud=%s", aud)
		}
	})

	t.Run("empty client ids never match", func(t *testing.T) {
		assert.False(t, tokenBoundToClient(jwt.MapClaims{"aud": "", "azp": ""}, []string{""}))
		assert.False(t, tokenBoundToClient(jwt.MapClaims{"azp": clientID}, nil))
	})

	t.Run("missing claims rejected", func(t *testing.T) {
		assert.False(t, tokenBoundToClient(jwt.MapClaims{}, []string{clientID}))
	})
}
