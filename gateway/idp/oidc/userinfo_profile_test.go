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
	"sync/atomic"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// fakeIssuer is an identity provider with an opaque access token: it answers
// the code exchange, the JWKS and the userinfo call.
type fakeIssuer struct {
	srv *httptest.Server
	key *rsa.PrivateKey

	// idClaims are added to the ID token beside iss, aud, iat and exp.
	idClaims map[string]any
	// userInfo is the userinfo answer, sent with userInfoStatus.
	userInfo       map[string]any
	userInfoStatus int
	userInfoCalls  atomic.Int32
}

const fakeIssuerKeyID = "k1"

func newFakeIssuer(t *testing.T, idClaims, userInfo map[string]any) *fakeIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	f := &fakeIssuer{key: key, idClaims: idClaims, userInfo: userInfo, userInfoStatus: http.StatusOK}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, _ *http.Request) {
		claims := jwt.MapClaims{
			"iss": f.srv.URL,
			"aud": "hoop",
			"iat": time.Now().Unix(),
			"exp": time.Now().Add(time.Minute).Unix(),
		}
		for k, v := range f.idClaims {
			claims[k] = v
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tok.Header["kid"] = fakeIssuerKeyID
		idToken, err := tok.SignedString(f.key)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "opaque-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
			"id_token":     idToken,
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, _ *http.Request) {
		pub := f.key.PublicKey
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "alg": "RS256", "use": "sig", "kid": fakeIssuerKeyID,
			"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("GET /userinfo", func(w http.ResponseWriter, r *http.Request) {
		f.userInfoCalls.Add(1)
		if r.Header.Get("Authorization") != "Bearer opaque-token" {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.userInfoStatus)
		_ = json.NewEncoder(w).Encode(f.userInfo)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIssuer) provider() *Provider {
	ctx := context.Background()
	op := (&oidc.ProviderConfig{
		IssuerURL:   f.srv.URL,
		TokenURL:    f.srv.URL + "/token",
		UserInfoURL: f.srv.URL + "/userinfo",
		JWKSURL:     f.srv.URL + "/jwks",
		Algorithms:  []string{"RS256"},
	}).NewProvider(ctx)
	return &Provider{
		Options:         Options{IssuerURL: f.srv.URL, ClientID: "hoop", ClientSecret: "secret", GroupsClaim: "groups"},
		context:         ctx,
		oidcProvider:    op,
		idTokenVerifier: op.Verifier(&oidc.Config{ClientID: "hoop"}),
		oauth2Config: oauth2.Config{
			ClientID:     "hoop",
			ClientSecret: "secret",
			Endpoint:     oauth2.Endpoint{TokenURL: f.srv.URL + "/token"},
		},
	}
}

// A provider can keep the email, the name and the groups out of the ID token
// and serve them at the userinfo endpoint, as a PingFederate policy does.
func TestSignInCompletesTheProfileFromUserInfo(t *testing.T) {
	t.Run("the ID token carries the subject alone", func(t *testing.T) {
		f := newFakeIssuer(t,
			map[string]any{"sub": "bob"},
			map[string]any{"sub": "bob", "email": "Bob@Acme.com", "name": "Bob", "groups": []string{"dba", "desk-7"}})

		_, uinfo, err := f.provider().VerifyIDTokenForCode("the-code")
		require.NoError(t, err)
		assert.Equal(t, "bob@acme.com", uinfo.Email)
		assert.Equal(t, "Bob", uinfo.Profile)
		assert.Equal(t, []string{"dba", "desk-7"}, uinfo.Groups)
		assert.True(t, uinfo.MustSyncGroups, "the groups of the userinfo answer replace the stored ones")
	})

	// A provider that sends the email in the ID token keeps today's login:
	// no userinfo call, and no name or groups taken from it.
	t.Run("an ID token with an email is left as it is", func(t *testing.T) {
		userInfo := map[string]any{"sub": "bob", "email": "other@acme.com", "name": "Other", "groups": []string{"admin"}}
		for name, tc := range map[string]struct {
			idToken    map[string]any
			wantName   string
			wantGroups []string
		}{
			"complete":  {map[string]any{"sub": "bob", "email": "bob@acme.com", "name": "Bob", "groups": []string{"dba"}}, "Bob", []string{"dba"}},
			"no groups": {map[string]any{"sub": "bob", "email": "bob@acme.com", "name": "Bob"}, "Bob", nil},
			"no name":   {map[string]any{"sub": "bob", "email": "bob@acme.com", "groups": []string{"dba"}}, "", []string{"dba"}},
		} {
			t.Run(name, func(t *testing.T) {
				f := newFakeIssuer(t, tc.idToken, userInfo)

				_, uinfo, err := f.provider().VerifyIDTokenForCode("the-code")
				require.NoError(t, err)
				assert.Equal(t, "bob@acme.com", uinfo.Email)
				assert.Equal(t, tc.wantName, uinfo.Profile)
				assert.Equal(t, tc.wantGroups, uinfo.Groups)
				assert.Equal(t, tc.wantGroups != nil, uinfo.MustSyncGroups)
				assert.Zero(t, f.userInfoCalls.Load())
			})
		}
	})

	t.Run("the answer of another subject is not used", func(t *testing.T) {
		f := newFakeIssuer(t,
			map[string]any{"sub": "bob"},
			map[string]any{"sub": "mallory", "email": "mallory@acme.com", "groups": []string{"admin"}})

		_, uinfo, err := f.provider().VerifyIDTokenForCode("the-code")
		require.NoError(t, err)
		assert.Empty(t, uinfo.Email)
		assert.Empty(t, uinfo.Groups)
		assert.False(t, uinfo.MustSyncGroups)
	})

	t.Run("a failing userinfo endpoint leaves the ID token's values", func(t *testing.T) {
		f := newFakeIssuer(t,
			map[string]any{"sub": "bob", "name": "Bob"},
			map[string]any{"error": "unavailable"})
		f.userInfoStatus = http.StatusServiceUnavailable

		_, uinfo, err := f.provider().VerifyIDTokenForCode("the-code")
		require.NoError(t, err)
		assert.Empty(t, uinfo.Email)
		assert.Equal(t, "Bob", uinfo.Profile)
		assert.False(t, uinfo.MustSyncGroups, "the stored groups stay when the provider sent none")
	})
}
