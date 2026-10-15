package oidc_test

// The contract suite: what hoop needs from an OIDC provider
// (gateway/idp/oidc). It runs against an in-process twin. When
// TWINS_OIDC_ISSUER is set, the same checks run against that real issuer
// instead, and a difference means the twin is wrong. The README names the
// variables.

import (
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/twins/oidc"
)

// contractTarget is an OIDC provider the suite checks.
type contractTarget struct {
	issuer, clientID, clientSecret string
	groupsClaim                    string
	// refreshToken belongs to a user in at least one group. The twin gets one
	// through the code flow; a real issuer cannot do that without a browser,
	// so it comes from the environment.
	refreshToken string
	client       *http.Client
}

func newContractTarget(t *testing.T) contractTarget {
	t.Helper()
	if issuer := os.Getenv("TWINS_OIDC_ISSUER"); issuer != "" {
		tg := contractTarget{issuer: strings.TrimSuffix(issuer, "/"),
			clientID: os.Getenv("TWINS_OIDC_CLIENT_ID"), clientSecret: os.Getenv("TWINS_OIDC_CLIENT_SECRET"),
			refreshToken: os.Getenv("TWINS_OIDC_REFRESH_TOKEN"), groupsClaim: os.Getenv("TWINS_OIDC_GROUPS_CLAIM"),
			client: &http.Client{Timeout: 30 * time.Second}}
		if tg.clientID == "" || tg.clientSecret == "" || tg.refreshToken == "" {
			t.Fatal("TWINS_OIDC_ISSUER is set: also set TWINS_OIDC_CLIENT_ID, TWINS_OIDC_CLIENT_SECRET and TWINS_OIDC_REFRESH_TOKEN")
		}
		if tg.groupsClaim == "" {
			tg.groupsClaim = oidc.DefaultGroupsClaim
		}
		t.Logf("contract target: real issuer %s", tg.issuer)
		return tg
	}
	s := startTwin(t)
	s.seed(t, `{"users":[{"sub":"contract-sub","email":"contract@example.com","name":"Contract","groups":["sre","dev"]}]}`)
	tok := s.login(t, "contract@example.com")
	return contractTarget{issuer: s.issuer, clientID: "hoop", clientSecret: "hoop-secret",
		groupsClaim: oidc.DefaultGroupsClaim, refreshToken: tok.RefreshToken, client: s.client}
}

type discoveryDoc struct {
	Issuer           string   `json:"issuer"`
	AuthEndpoint     string   `json:"authorization_endpoint"`
	TokenEndpoint    string   `json:"token_endpoint"`
	UserinfoEndpoint string   `json:"userinfo_endpoint"`
	JWKSURI          string   `json:"jwks_uri"`
	ResponseTypes    []string `json:"response_types_supported"`
	SigningAlgs      []string `json:"id_token_signing_alg_values_supported"`
}

func (tg contractTarget) discovery(t *testing.T) discoveryDoc {
	t.Helper()
	var doc discoveryDoc
	if code := getJSON(t, tg.client, tg.issuer+"/.well-known/openid-configuration", "", &doc); code != http.StatusOK {
		t.Fatalf("discovery: status %d", code)
	}
	return doc
}

func (tg contractTarget) jwks(t *testing.T) []jwk {
	t.Helper()
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if code := getJSON(t, tg.client, tg.discovery(t).JWKSURI, "", &set); code != http.StatusOK {
		t.Fatalf("jwks: status %d", code)
	}
	return set.Keys
}

func (tg contractTarget) tokenRequest(t *testing.T, form url.Values) (int, tokenAnswer) {
	t.Helper()
	var ans tokenAnswer
	code := postForm(t, tg.client, tg.discovery(t).TokenEndpoint, form, &ans)
	return code, ans
}

// The gateway refuses a document whose issuer differs from the configured
// one (gateway/idp/oidc/provider.go), and needs every endpoint it calls.
func TestContractDiscovery(t *testing.T) {
	tg := newContractTarget(t)
	doc := tg.discovery(t)
	if doc.Issuer != tg.issuer {
		t.Errorf("issuer %q, want exactly %q", doc.Issuer, tg.issuer)
	}
	for name, u := range map[string]string{"authorization_endpoint": doc.AuthEndpoint, "token_endpoint": doc.TokenEndpoint,
		"userinfo_endpoint": doc.UserinfoEndpoint, "jwks_uri": doc.JWKSURI} {
		if p, err := url.Parse(u); err != nil || p.Scheme != "https" || p.Host == "" {
			t.Errorf("%s %q: want an https URL", name, u)
		}
	}
	if !slices.Contains(doc.ResponseTypes, "code") {
		t.Errorf("response_types_supported %v: want code", doc.ResponseTypes)
	}
	if !slices.Contains(doc.SigningAlgs, "RS256") {
		t.Errorf("id_token_signing_alg_values_supported %v: want RS256", doc.SigningAlgs)
	}
}

// The gateway downloads the JWKS for every provider it loads
// (gateway/idp/oidc/oidc.go downloadJWKS) and verifies RS256 signatures.
func TestContractJWKS(t *testing.T) {
	tg := newContractTarget(t)
	keys := tg.jwks(t)
	var rsaSig int
	for _, k := range keys {
		if k.Kty == "RSA" && k.Use != "enc" {
			if k.Kid == "" || k.N == "" || k.E == "" {
				t.Errorf("RSA key %+v: want kid, n and e", k)
			}
			rsaSig++
		}
	}
	if rsaSig == 0 {
		t.Fatalf("no RSA signing key in %+v", keys)
	}
}

// A failed exchange answers 4xx with an OAuth error body (RFC 6749 5.2).
// The gateway only needs the failure; the codes help a person read the log.
func TestContractTokenErrors(t *testing.T) {
	tg := newContractTarget(t)
	code, ans := tg.tokenRequest(t, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tg.refreshToken},
		"client_id": {tg.clientID}, "client_secret": {"not-the-secret"}})
	if code < 400 || code > 499 || ans.Error == "" || ans.AccessToken != "" {
		t.Errorf("wrong client secret: got %d %+v, want 4xx with an error code", code, ans)
	}
	code, ans = tg.tokenRequest(t, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {"not-a-refresh-token"},
		"client_id": {tg.clientID}, "client_secret": {tg.clientSecret}})
	if code < 400 || code > 499 || ans.Error != "invalid_grant" {
		t.Errorf("unknown refresh token: got %d %+v, want 4xx invalid_grant", code, ans)
	}
}

// A refresh, then the userinfo group sync, is how hoop learns of a group
// change (gateway/idp/core.go refreshForSubject, syncUserGroupsFromUserInfo).
func TestContractRefreshAndUserinfo(t *testing.T) {
	tg := newContractTarget(t)
	code, ans := tg.tokenRequest(t, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tg.refreshToken},
		"client_id": {tg.clientID}, "client_secret": {tg.clientSecret}})
	if code != http.StatusOK || ans.AccessToken == "" {
		t.Fatalf("refresh: got %d %+v, want 200 with an access_token", code, ans)
	}
	if !strings.EqualFold(ans.TokenType, "bearer") {
		t.Errorf("token_type %q, want Bearer", ans.TokenType)
	}
	keys := tg.jwks(t)

	// A JWT access token is verified with the JWKS; an opaque one goes to
	// userinfo (gateway/idp/oidc/oidc.go VerifyAccessToken).
	if strings.Count(ans.AccessToken, ".") == 2 {
		if err := verifyRS256(ans.AccessToken, keys); err != nil {
			t.Errorf("access token signature: %v", err)
		}
	}
	if ans.IDToken != "" {
		if err := verifyRS256(ans.IDToken, keys); err != nil {
			t.Errorf("id_token signature: %v", err)
		}
		claims := jwtClaims(t, ans.IDToken)
		if claims["iss"] != tg.issuer {
			t.Errorf("id_token iss %v, want %s", claims["iss"], tg.issuer)
		}
		if !audHas(claims["aud"], tg.clientID) {
			t.Errorf("id_token aud %v, want it to hold %s", claims["aud"], tg.clientID)
		}
		if azp, ok := claims["azp"]; ok && azp != tg.clientID {
			t.Errorf("id_token azp %v: the gateway refuses an azp other than the client id", azp)
		}
		if exp, _ := claims["exp"].(float64); int64(exp) <= time.Now().Unix() {
			t.Errorf("id_token exp %v is not in the future", claims["exp"])
		}
	}

	var info map[string]any
	if code := getJSON(t, tg.client, tg.discovery(t).UserinfoEndpoint, ans.AccessToken, &info); code != http.StatusOK {
		t.Fatalf("userinfo: status %d", code)
	}
	if sub, _ := info["sub"].(string); sub == "" {
		t.Errorf("userinfo has no sub: %v", info)
	}
	if email, _ := info["email"].(string); email == "" {
		t.Errorf("userinfo has no email: %v", info)
	}
	switch g := info[tg.groupsClaim].(type) {
	case []any:
		if len(g) == 0 {
			t.Errorf("userinfo %s is empty: the token's user must be in a group", tg.groupsClaim)
		}
	case string:
	default:
		t.Errorf("userinfo %s is %T (%v): want a list or a string, or the gateway never syncs groups", tg.groupsClaim, g, g)
	}

	if code := getJSON(t, tg.client, tg.discovery(t).UserinfoEndpoint, "not-a-token", nil); code != http.StatusUnauthorized {
		t.Errorf("userinfo with a bad token: status %d, want 401", code)
	}
}

func audHas(aud any, want string) bool {
	switch v := aud.(type) {
	case string:
		return v == want
	case []any:
		return slices.Contains(v, any(want))
	}
	return false
}
