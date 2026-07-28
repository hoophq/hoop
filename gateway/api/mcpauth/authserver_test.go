package mcpauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func resetIdpMetadataCache() {
	idpMetadataCache.mu.Lock()
	defer idpMetadataCache.mu.Unlock()
	idpMetadataCache.issuer = ""
	idpMetadataCache.fetchedAt = time.Time{}
	idpMetadataCache.meta = nil
}

func TestBuildAuthServerMetadata(t *testing.T) {
	idpMeta := &idpAuthServerMetadata{
		Issuer:                "https://idp.example.com",
		AuthorizationEndpoint: "https://idp.example.com/oauth2/auth",
		TokenEndpoint:         "https://idp.example.com/oauth2/token",
		JwksURI:               "https://idp.example.com/oauth2/keys",
		ScopesSupported:       []string{"openid", "profile"},
	}

	t.Run("public client", func(t *testing.T) {
		got := buildAuthServerMetadata(
			effectiveConfig{ClientID: "hoop-mcp"}, idpMeta,
			"https://gw.example.com", "https://gw.example.com/api/mcp/oauth/register")

		// Issuer is the gateway (RFC 8414 requires it to match the URL the
		// client derived the well-known path from), endpoints are the IdP's.
		assert.Equal(t, "https://gw.example.com", got.Issuer)
		assert.Equal(t, idpMeta.AuthorizationEndpoint, got.AuthorizationEndpoint)
		assert.Equal(t, idpMeta.TokenEndpoint, got.TokenEndpoint)
		assert.Equal(t, idpMeta.JwksURI, got.JwksURI)
		assert.Equal(t, "https://gw.example.com/api/mcp/oauth/register", got.RegistrationEndpoint)
		assert.Equal(t, []string{"none"}, got.TokenEndpointAuthMethodsSupported)
		// Defaults filled when the IdP omits them; S256 force-advertised.
		assert.Equal(t, []string{"code"}, got.ResponseTypesSupported)
		assert.Equal(t, []string{"authorization_code", "refresh_token"}, got.GrantTypesSupported)
		assert.Equal(t, []string{"S256"}, got.CodeChallengeMethodsSupported)
	})

	t.Run("confidential client", func(t *testing.T) {
		got := buildAuthServerMetadata(
			effectiveConfig{ClientID: "hoop-mcp", ClientSecret: "s3cr3t"}, idpMeta,
			"https://gw.example.com", "https://gw.example.com/api/mcp/oauth/register")
		assert.Equal(t, []string{"client_secret_basic", "client_secret_post"}, got.TokenEndpointAuthMethodsSupported)
	})

	t.Run("idp advertised values pass through", func(t *testing.T) {
		meta := *idpMeta
		meta.ResponseTypesSupported = []string{"code", "token"}
		meta.GrantTypesSupported = []string{"authorization_code"}
		meta.CodeChallengeMethodsSupported = []string{"plain", "S256"}
		got := buildAuthServerMetadata(effectiveConfig{ClientID: "x"}, &meta, "https://gw", "https://gw/reg")
		assert.Equal(t, []string{"code", "token"}, got.ResponseTypesSupported)
		assert.Equal(t, []string{"authorization_code"}, got.GrantTypesSupported)
		assert.Equal(t, []string{"plain", "S256"}, got.CodeChallengeMethodsSupported)
	})
}

func TestBuildRegistrationResponse(t *testing.T) {
	req := clientRegistration{
		RedirectURIs: []string{"http://localhost:33418/callback"},
		ClientName:   "Claude Code",
		Scope:        "openid profile",
	}

	t.Run("public client", func(t *testing.T) {
		got := buildRegistrationResponse(effectiveConfig{ClientID: "hoop-mcp"}, req)
		assert.Equal(t, "hoop-mcp", got.ClientID)
		assert.Empty(t, got.ClientSecret)
		assert.Nil(t, got.ClientSecretExpiresAt)
		assert.Equal(t, "none", got.TokenEndpointAuthMethod)
		// Caller metadata echoed back per RFC 7591.
		assert.Equal(t, req.RedirectURIs, got.RedirectURIs)
		assert.Equal(t, req.ClientName, got.ClientName)
		assert.Equal(t, req.Scope, got.Scope)
		assert.Equal(t, []string{"authorization_code", "refresh_token"}, got.GrantTypes)
		assert.Equal(t, []string{"code"}, got.ResponseTypes)
	})

	t.Run("confidential client", func(t *testing.T) {
		got := buildRegistrationResponse(effectiveConfig{ClientID: "hoop-mcp", ClientSecret: "s3cr3t"}, req)
		assert.Equal(t, "s3cr3t", got.ClientSecret)
		if assert.NotNil(t, got.ClientSecretExpiresAt) {
			assert.Equal(t, int64(0), *got.ClientSecretExpiresAt) // never expires
		}
		assert.Equal(t, "client_secret_basic", got.TokenEndpointAuthMethod)
	})

	t.Run("caller grant and response types pass through", func(t *testing.T) {
		r := req
		r.GrantTypes = []string{"authorization_code"}
		r.ResponseTypes = []string{"code"}
		got := buildRegistrationResponse(effectiveConfig{ClientID: "x"}, r)
		assert.Equal(t, []string{"authorization_code"}, got.GrantTypes)
	})
}

func TestFetchIdpMetadata(t *testing.T) {
	newIdp := func(t *testing.T, openidStatus int, hits *int) *httptest.Server {
		t.Helper()
		var srv *httptest.Server
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*hits++
			doc := map[string]any{
				"issuer":                 srv.URL,
				"authorization_endpoint": srv.URL + "/authorize",
				"token_endpoint":         srv.URL + "/token",
			}
			switch r.URL.Path {
			case "/.well-known/openid-configuration":
				if openidStatus != http.StatusOK {
					w.WriteHeader(openidStatus)
					return
				}
			case "/.well-known/oauth-authorization-server":
			default:
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(doc)
		}))
		t.Cleanup(srv.Close)
		return srv
	}

	t.Run("openid discovery", func(t *testing.T) {
		resetIdpMetadataCache()
		hits := 0
		srv := newIdp(t, http.StatusOK, &hits)
		meta, err := fetchIdpMetadata(context.Background(), srv.URL)
		assert.NoError(t, err)
		assert.Equal(t, srv.URL+"/authorize", meta.AuthorizationEndpoint)
		assert.Equal(t, srv.URL+"/token", meta.TokenEndpoint)
		assert.Equal(t, 1, hits)
	})

	t.Run("trailing slash issuer accepted", func(t *testing.T) {
		resetIdpMetadataCache()
		hits := 0
		srv := newIdp(t, http.StatusOK, &hits)
		_, err := fetchIdpMetadata(context.Background(), srv.URL+"/")
		assert.NoError(t, err)
	})

	t.Run("falls back to rfc8414 well-known", func(t *testing.T) {
		resetIdpMetadataCache()
		hits := 0
		srv := newIdp(t, http.StatusNotFound, &hits)
		meta, err := fetchIdpMetadata(context.Background(), srv.URL)
		assert.NoError(t, err)
		assert.Equal(t, srv.URL+"/token", meta.TokenEndpoint)
		assert.Equal(t, 2, hits)
	})

	t.Run("second call served from cache", func(t *testing.T) {
		resetIdpMetadataCache()
		hits := 0
		srv := newIdp(t, http.StatusOK, &hits)
		_, err := fetchIdpMetadata(context.Background(), srv.URL)
		assert.NoError(t, err)
		_, err = fetchIdpMetadata(context.Background(), srv.URL)
		assert.NoError(t, err)
		assert.Equal(t, 1, hits)
	})

	t.Run("issuer mismatch rejected", func(t *testing.T) {
		resetIdpMetadataCache()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"issuer":"https://evil.example.com","authorization_endpoint":"https://evil.example.com/a","token_endpoint":"https://evil.example.com/t"}`)
		}))
		defer srv.Close()
		_, err := fetchIdpMetadata(context.Background(), srv.URL)
		assert.ErrorContains(t, err, "issuer mismatch")
	})

	t.Run("missing endpoints rejected", func(t *testing.T) {
		resetIdpMetadataCache()
		var srv *httptest.Server
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"issuer":%q}`, srv.URL)
		}))
		defer srv.Close()
		_, err := fetchIdpMetadata(context.Background(), srv.URL)
		assert.ErrorContains(t, err, "missing authorization_endpoint")
	})
}
