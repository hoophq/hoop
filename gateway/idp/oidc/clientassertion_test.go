package oidcprovider

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeIdP is an OIDC provider that records the form and the Basic auth of
// each token request.
type fakeIdP struct {
	*httptest.Server
	key *rsa.PrivateKey

	mu       sync.Mutex
	requests []tokenRequest
}

type tokenRequest struct {
	form       url.Values
	basicAuth  bool
	basicID    string
	basicToken string
}

func newFakeIdP(t *testing.T) *fakeIdP {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	idp := &fakeIdP{key: key}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                idp.URL,
			"authorization_endpoint":                idp.URL + "/authorize",
			"token_endpoint":                        idp.URL + "/token",
			"userinfo_endpoint":                     idp.URL + "/userinfo",
			"jwks_uri":                              idp.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "k1", "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		id, secret, ok := r.BasicAuth()
		idp.mu.Lock()
		idp.requests = append(idp.requests, tokenRequest{form: r.PostForm, basicAuth: ok, basicID: id, basicToken: secret})
		idp.mu.Unlock()
		idToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
			"iss": idp.URL, "sub": "user-1", "aud": "client-id", "email": "alice@example.com",
			"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
		})
		idToken.Header["kid"] = "k1"
		signed, err := idToken.SignedString(key)
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "access", "token_type": "Bearer", "expires_in": 3600,
			"refresh_token": "refresh", "id_token": signed,
		})
	})
	idp.Server = httptest.NewServer(mux)
	t.Cleanup(idp.Close)
	return idp
}

func (f *fakeIdP) lastRequest(t *testing.T) tokenRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	require.NotEmpty(t, f.requests)
	return f.requests[len(f.requests)-1]
}

func writeAssertion(t *testing.T, path, content string) {
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))
}

func assertClientAssertion(t *testing.T, req tokenRequest, grantType, assertion string) {
	t.Helper()
	assert.Equal(t, grantType, req.form.Get("grant_type"))
	assert.False(t, req.basicAuth, "no Basic auth header with a client assertion")
	assert.Equal(t, "client-id", req.form.Get("client_id"))
	assert.Equal(t, jwtBearerAssertionType, req.form.Get("client_assertion_type"))
	assert.Equal(t, assertion, req.form.Get("client_assertion"))
	assert.NotContains(t, req.form, "client_secret")
}

func TestClientAssertionTokenRequests(t *testing.T) {
	idp := newFakeIdP(t)
	file := filepath.Join(t.TempDir(), "token")
	writeAssertion(t, file, "assertion-1\n")

	p, err := New(Options{IssuerURL: idp.URL, ClientID: "client-id", ClientAssertionFile: file})
	require.NoError(t, err)

	t.Run("authorization code exchange", func(t *testing.T) {
		_, uinfo, err := p.VerifyIDTokenForCode("code-1")
		require.NoError(t, err)
		assert.Equal(t, "alice@example.com", uinfo.Email)
		req := idp.lastRequest(t)
		assertClientAssertion(t, req, "authorization_code", "assertion-1")
		assert.Equal(t, "code-1", req.form.Get("code"))
	})

	t.Run("refresh", func(t *testing.T) {
		tok, err := p.RefreshAccessToken(context.Background(), "refresh-1")
		require.NoError(t, err)
		assert.Equal(t, "access", tok.AccessToken)
		req := idp.lastRequest(t)
		assertClientAssertion(t, req, "refresh_token", "assertion-1")
		assert.Equal(t, "refresh-1", req.form.Get("refresh_token"))
	})

	t.Run("client credentials", func(t *testing.T) {
		_, err := p.clientCredentialsToken(context.Background(), []string{graphScope}, "")
		require.NoError(t, err)
		req := idp.lastRequest(t)
		assertClientAssertion(t, req, "client_credentials", "assertion-1")
		assert.Equal(t, graphScope, req.form.Get("scope"))
	})

	t.Run("rotated file is read on the next request", func(t *testing.T) {
		writeAssertion(t, file, "assertion-2")
		_, err := p.RefreshAccessToken(context.Background(), "refresh-1")
		require.NoError(t, err)
		assertClientAssertion(t, idp.lastRequest(t), "refresh_token", "assertion-2")
	})

	t.Run("empty file fails the request", func(t *testing.T) {
		writeAssertion(t, file, "")
		_, err := p.RefreshAccessToken(context.Background(), "refresh-1")
		assert.ErrorContains(t, err, "is empty")
	})
}

func TestClientSecretTokenRequestsUnchanged(t *testing.T) {
	idp := newFakeIdP(t)
	p, err := New(Options{IssuerURL: idp.URL, ClientID: "client-id", ClientSecret: "secret"})
	require.NoError(t, err)

	_, _, err = p.VerifyIDTokenForCode("code-1")
	require.NoError(t, err)
	_, err = p.RefreshAccessToken(context.Background(), "refresh-1")
	require.NoError(t, err)
	_, err = p.clientCredentialsToken(context.Background(), []string{graphScope}, "")
	require.NoError(t, err)

	idp.mu.Lock()
	defer idp.mu.Unlock()
	require.Len(t, idp.requests, 3)
	for _, req := range idp.requests {
		assert.True(t, req.basicAuth, "grant=%v", req.form.Get("grant_type"))
		assert.Equal(t, "client-id", req.basicID)
		assert.Equal(t, "secret", req.basicToken)
		assert.NotContains(t, req.form, "client_assertion")
		assert.NotContains(t, req.form, "client_assertion_type")
	}
}

func TestOptionsValidateClientCredentials(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "token")
	writeAssertion(t, file, "assertion")
	emptyFile := filepath.Join(dir, "empty")
	writeAssertion(t, emptyFile, " \n")
	base := Options{IssuerURL: "https://idp.example.com", ClientID: "client-id"}

	for _, tc := range []struct {
		name    string
		secret  string
		file    string
		wantErr string
	}{
		{name: "secret only", secret: "secret"},
		{name: "assertion file only", file: file},
		{name: "neither", wantErr: "missing required oidc attributes (issuer-url, client-id, client-secret)"},
		{name: "both", secret: "secret", file: file, wantErr: "not both"},
		{name: "missing file", file: filepath.Join(dir, "missing"), wantErr: "failed reading client assertion file"},
		{name: "empty file", file: emptyFile, wantErr: "is empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := base
			opts.ClientSecret, opts.ClientAssertionFile = tc.secret, tc.file
			err := opts.validate()
			if tc.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestParseOptionsFromEnvClientAssertion(t *testing.T) {
	t.Run("IDP_ISSUER", func(t *testing.T) {
		t.Setenv("IDP_URI", "")
		t.Setenv("IDP_ISSUER", "https://login.microsoftonline.com/tenant/v2.0")
		t.Setenv("IDP_CLIENT_ID", "client-id")
		t.Setenv("IDP_CLIENT_SECRET", "")
		t.Setenv("IDP_CLIENT_ASSERTION_FILE", "/var/run/secrets/token")
		opts, err := ParseOptionsFromEnv()
		require.NoError(t, err)
		assert.Equal(t, "client-id", opts.ClientID)
		assert.Empty(t, opts.ClientSecret)
		assert.Equal(t, "/var/run/secrets/token", opts.ClientAssertionFile)
	})

	t.Run("IDP_URI without a secret", func(t *testing.T) {
		t.Setenv("IDP_URI", "https://client-id@login.microsoftonline.com/tenant/v2.0")
		t.Setenv("IDP_CLIENT_ASSERTION_FILE", "/var/run/secrets/token")
		opts, err := ParseOptionsFromEnv()
		require.NoError(t, err)
		assert.Equal(t, "client-id", opts.ClientID)
		assert.Empty(t, opts.ClientSecret)
		assert.Equal(t, "/var/run/secrets/token", opts.ClientAssertionFile)
		assert.Equal(t, "https://login.microsoftonline.com/tenant/v2.0", opts.IssuerURL)
	})

	t.Run("IDP_URI without a secret or an assertion file", func(t *testing.T) {
		t.Setenv("IDP_URI", "https://client-id@login.microsoftonline.com/tenant/v2.0")
		t.Setenv("IDP_CLIENT_ASSERTION_FILE", "")
		_, err := ParseOptionsFromEnv()
		assert.ErrorContains(t, err, "missing credentials for IDP_URI env")
	})
}
