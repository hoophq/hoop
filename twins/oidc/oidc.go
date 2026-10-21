// Package oidc is a twin of an OpenID Connect provider: discovery,
// authorize, token (authorization_code and refresh_token), userinfo and
// JWKS. Users, groups and the groups claim name come from /_twin/seed.
//
// Authorize approves the seeded user that the standard login_hint parameter
// names, so a scenario logs in over HTTP with no browser. With no
// login_hint, it serves a page that lists the seeded users, for a person or
// a headless browser to pick one.
package oidc

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hoophq/hoop/twins/kit"
)

// DefaultGroupsClaim is the claim hoop reads when IDP_GROUPS_CLAIM is not
// set (common/proto/const.go).
const DefaultGroupsClaim = "https://app.hoop.dev/groups"

// Token kinds, as /_twin/state and the expired_tokens seed field name them.
const (
	KindAccessToken  = "access_token"
	KindIDToken      = "id_token"
	KindRefreshToken = "refresh_token"
)

// User is a seeded identity. Email selects the user in login_hint.
type User struct {
	Sub    string   `json:"sub"`
	Email  string   `json:"email"`
	Name   string   `json:"name"`
	Groups []string `json:"groups"`
}

// seed is the body of POST /_twin/seed. A zero field keeps the current
// value. Users are added, or replaced when the email is already seeded.
type seed struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	GroupsClaim  string `json:"groups_claim"`
	TokenTTLSec  int    `json:"token_ttl_sec"`
	// ExpiredTokens lists the kinds (access_token, id_token) the token
	// endpoint issues already expired. An empty list clears it; an absent
	// field keeps it.
	ExpiredTokens []string `json:"expired_tokens"`
	Users         []User   `json:"users"`
}

type code struct {
	Code        string    `json:"code"`
	Email       string    `json:"email"`
	RedirectURI string    `json:"redirect_uri"`
	Nonce       string    `json:"nonce,omitempty"`
	IssuedAt    time.Time `json:"issued_at"`
	Used        bool      `json:"used"`
}

// issued records one token the twin handed out. It keeps the last 8
// characters only, enough to match a log line.
type issued struct {
	Kind      string    `json:"kind"`
	Sub       string    `json:"sub"`
	Grant     string    `json:"grant"`
	Suffix    string    `json:"suffix"`
	ExpiresAt time.Time `json:"expires_at,omitzero"`
	Expired   bool      `json:"expired,omitempty"`
}

// Twin is the OIDC provider twin. Create it with New.
type Twin struct {
	issuer string
	// browserURL is where a browser reaches the twin. Discovery advertises
	// the authorize endpoint there; the gateway never calls it.
	browserURL string
	key        *rsa.PrivateKey
	kid        string

	mu      sync.Mutex
	cfg     seed
	expired map[string]bool
	users   map[string]User // by lower-case email
	codes   map[string]*code
	refresh map[string]string // refresh token -> lower-case email
	access  map[string]bool   // jti of every access token issued
	issued  []issued
}

// New returns a twin that answers as issuer. The gateway requires the
// discovery document to name the issuer it is configured with.
//
// browserURL is the base URL of the authorize endpoint in discovery, for a
// browser that cannot resolve or trust the issuer host. Empty means the
// issuer.
func New(issuer, browserURL string) (*Twin, error) {
	issuer = strings.TrimSuffix(issuer, "/")
	if browserURL == "" {
		browserURL = issuer
	}
	if u, err := url.Parse(browserURL); err != nil || !u.IsAbs() || u.Host == "" {
		return nil, fmt.Errorf("browser URL %q must be an absolute URL", browserURL)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	t := &Twin{issuer: issuer, browserURL: strings.TrimSuffix(browserURL, "/"), key: key, kid: "twin-1"}
	t.Reset()
	return t, nil
}

func (t *Twin) Name() string { return "oidc" }

func (t *Twin) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cfg = seed{ClientID: "hoop", ClientSecret: "hoop-secret", GroupsClaim: DefaultGroupsClaim, TokenTTLSec: 3600}
	t.expired = map[string]bool{}
	t.users = map[string]User{}
	t.codes = map[string]*code{}
	t.refresh = map[string]string{}
	t.access = map[string]bool{}
	t.issued = nil
}

func (t *Twin) Seed(raw json.RawMessage) error {
	var s seed
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return err
	}
	if (s.ClientID == "") != (s.ClientSecret == "") {
		return fmt.Errorf("set client_id and client_secret together")
	}
	if s.TokenTTLSec < 0 {
		return fmt.Errorf("token_ttl_sec must be positive; use expired_tokens for expired tokens")
	}
	for _, k := range s.ExpiredTokens {
		if k != KindAccessToken && k != KindIDToken {
			return fmt.Errorf("expired_tokens takes %s or %s, got %q", KindAccessToken, KindIDToken, k)
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	// userinfo finds the user by sub, so one sub must name one email.
	owner := map[string]string{}
	for email, u := range t.users {
		owner[u.Sub] = email
	}
	for _, u := range s.Users {
		if u.Email == "" || u.Sub == "" {
			return fmt.Errorf("a user needs sub and email, got %+v", u)
		}
		email := strings.ToLower(u.Email)
		if other, ok := owner[u.Sub]; ok && other != email {
			return fmt.Errorf("sub %q already belongs to %s", u.Sub, other)
		}
		owner[u.Sub] = email
	}
	if s.ClientID != "" {
		t.cfg.ClientID, t.cfg.ClientSecret = s.ClientID, s.ClientSecret
	}
	if s.GroupsClaim != "" {
		t.cfg.GroupsClaim = s.GroupsClaim
	}
	if s.TokenTTLSec != 0 {
		t.cfg.TokenTTLSec = s.TokenTTLSec
	}
	if s.ExpiredTokens != nil {
		t.expired = map[string]bool{}
		for _, k := range s.ExpiredTokens {
			t.expired[k] = true
		}
	}
	for _, u := range s.Users {
		t.users[strings.ToLower(u.Email)] = u
	}
	return nil
}

// state is the document of /_twin/state. Its field order is the order of
// the /_twin/ui page.
type state struct {
	Issuer        string   `json:"issuer"`
	BrowserURL    string   `json:"browser_url"`
	ClientID      string   `json:"client_id"`
	GroupsClaim   string   `json:"groups_claim"`
	TokenTTLSec   int      `json:"token_ttl_sec"`
	Users         []User   `json:"users"`
	Codes         []code   `json:"codes"`
	Tokens        []issued `json:"tokens"`
	ExpiredTokens []string `json:"expired_tokens"`
}

func (t *Twin) State() any {
	t.mu.Lock()
	defer t.mu.Unlock()
	codes := make([]code, 0, len(t.codes))
	for _, c := range t.codes {
		codes = append(codes, *c)
	}
	sort.Slice(codes, func(i, j int) bool { return codes[i].IssuedAt.Before(codes[j].IssuedAt) })
	expired := make([]string, 0, len(t.expired))
	for k := range t.expired {
		expired = append(expired, k)
	}
	slices.Sort(expired)
	return state{Issuer: t.issuer, BrowserURL: t.browserURL, ClientID: t.cfg.ClientID, GroupsClaim: t.cfg.GroupsClaim,
		TokenTTLSec: t.cfg.TokenTTLSec, Users: t.sortedUsers(), Codes: codes,
		Tokens: append([]issued{}, t.issued...), ExpiredTokens: expired}
}

// sortedUsers lists the seeded users by email. The caller holds t.mu.
func (t *Twin) sortedUsers() []User {
	users := make([]User, 0, len(t.users))
	for _, u := range t.users {
		users = append(users, u)
	}
	sort.Slice(users, func(i, j int) bool { return users[i].Email < users[j].Email })
	return users
}

func (t *Twin) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /.well-known/openid-configuration", t.discovery)
	mux.HandleFunc("GET /authorize", t.authorize)
	mux.HandleFunc("POST /token", t.token)
	mux.HandleFunc("GET /userinfo", t.userinfo)
	mux.HandleFunc("POST /userinfo", t.userinfo)
	mux.HandleFunc("GET /jwks", t.jwks)
}

// authorizeURL is where a browser reaches authorize. Discovery advertises
// it, and the login page sends its form there.
func (t *Twin) authorizeURL() string { return t.browserURL + "/authorize" }

func (t *Twin) discovery(w http.ResponseWriter, _ *http.Request) {
	kit.WriteJSON(w, http.StatusOK, map[string]any{
		"issuer":                                t.issuer,
		"authorization_endpoint":                t.authorizeURL(),
		"token_endpoint":                        t.issuer + "/token",
		"userinfo_endpoint":                     t.issuer + "/userinfo",
		"jwks_uri":                              t.issuer + "/jwks",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"scopes_supported":                      []string{"openid", "email", "profile", "offline_access"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post"},
	})
}

func (t *Twin) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	t.mu.Lock()
	defer t.mu.Unlock()
	// RFC 6749 4.1.2.1: with no valid client or redirect_uri, answer here and
	// do not redirect.
	if q.Get("client_id") != t.cfg.ClientID {
		http.Error(w, "unknown client_id", http.StatusBadRequest)
		return
	}
	redirect, err := url.Parse(q.Get("redirect_uri"))
	if err != nil || !redirect.IsAbs() || redirect.Host == "" {
		http.Error(w, "redirect_uri must be an absolute URL", http.StatusBadRequest)
		return
	}
	fail := func(code, desc string) {
		rq := redirect.Query()
		rq.Set("error", code)
		rq.Set("error_description", desc)
		rq.Set("state", q.Get("state"))
		redirect.RawQuery = rq.Encode()
		http.Redirect(w, r, redirect.String(), http.StatusFound)
	}
	if q.Get("response_type") != "code" {
		fail("unsupported_response_type", "only response_type=code")
		return
	}
	if !slices.Contains(strings.Fields(q.Get("scope")), "openid") {
		fail("invalid_scope", "scope must hold openid")
		return
	}
	// A scenario names the user. A browser picks one on the login page,
	// which sends this request again with login_hint. An unknown user is a
	// broken scenario, so answer here with the reason.
	if !q.Has("login_hint") {
		t.loginPage(w, q)
		return
	}
	u, ok := t.users[strings.ToLower(q.Get("login_hint"))]
	if !ok {
		http.Error(w, fmt.Sprintf("login_hint %q names no seeded user", q.Get("login_hint")), http.StatusBadRequest)
		return
	}
	c := &code{Code: randomString(24), Email: strings.ToLower(u.Email), RedirectURI: q.Get("redirect_uri"),
		Nonce: q.Get("nonce"), IssuedAt: time.Now().UTC()}
	t.codes[c.Code] = c
	rq := redirect.Query()
	rq.Set("code", c.Code)
	rq.Set("state", q.Get("state"))
	redirect.RawQuery = rq.Encode()
	http.Redirect(w, r, redirect.String(), http.StatusFound)
}

func (t *Twin) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	id, secret, ok := r.BasicAuth()
	if ok {
		// RFC 6749 2.3.1: the client form-encodes both before Basic auth.
		id, _ = url.QueryUnescape(id)
		secret, _ = url.QueryUnescape(secret)
	} else {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if id != t.cfg.ClientID || secret != t.cfg.ClientSecret {
		w.Header().Set("WWW-Authenticate", `Basic realm="token"`)
		oauthError(w, http.StatusUnauthorized, "invalid_client", "client authentication failed")
		return
	}
	var email, nonce string
	grant := r.PostForm.Get("grant_type")
	switch grant {
	case "authorization_code":
		c, ok := t.codes[r.PostForm.Get("code")]
		if !ok || c.Used || c.RedirectURI != r.PostForm.Get("redirect_uri") {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "unknown, used or mismatched code")
			return
		}
		c.Used = true
		email, nonce = c.Email, c.Nonce
	case "refresh_token":
		email, ok = t.refresh[r.PostForm.Get("refresh_token")]
		if !ok {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "unknown refresh_token")
			return
		}
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type", grant)
		return
	}
	u, ok := t.users[email]
	if !ok {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "the user no longer exists")
		return
	}

	now := time.Now()
	ttl := time.Duration(t.cfg.TokenTTLSec) * time.Second
	expiry := func(kind string) time.Time {
		if t.expired[kind] {
			return now.Add(-time.Hour)
		}
		return now.Add(ttl)
	}
	accessExp, idExp := expiry(KindAccessToken), expiry(KindIDToken)
	// jti makes each token unique: two grants in the same second would
	// otherwise sign the same claims into the same bytes. userinfo accepts
	// only the jti of an access token.
	accessJTI := randomString(16)
	access, err := t.sign(map[string]any{"iss": t.issuer, "sub": u.Sub, "aud": t.cfg.ClientID, "client_id": t.cfg.ClientID,
		"scope": "openid email profile", "iat": now.Unix(), "exp": accessExp.Unix(), "jti": accessJTI})
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	idClaims := t.claims(u, map[string]any{"iss": t.issuer, "aud": t.cfg.ClientID, "azp": t.cfg.ClientID,
		"iat": now.Unix(), "exp": idExp.Unix(), "jti": randomString(16)})
	if nonce != "" {
		idClaims["nonce"] = nonce
	}
	idToken, err := t.sign(idClaims)
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	t.access[accessJTI] = true
	refresh := r.PostForm.Get("refresh_token")
	if refresh == "" {
		refresh = randomString(32)
		t.refresh[refresh] = email
	}
	t.record(KindAccessToken, u.Sub, grant, access, accessExp, t.expired[KindAccessToken])
	t.record(KindIDToken, u.Sub, grant, idToken, idExp, t.expired[KindIDToken])
	t.record(KindRefreshToken, u.Sub, grant, refresh, time.Time{}, false)
	w.Header().Set("Cache-Control", "no-store")
	kit.WriteJSON(w, http.StatusOK, map[string]any{"access_token": access, "id_token": idToken,
		"refresh_token": refresh, "token_type": "Bearer", "expires_in": int(time.Until(accessExp).Seconds())})
}

func (t *Twin) record(kind, sub, grant, tok string, exp time.Time, expired bool) {
	if !exp.IsZero() {
		exp = exp.UTC()
	}
	t.issued = append(t.issued, issued{Kind: kind, Sub: sub, Grant: grant, Suffix: tok[len(tok)-8:], ExpiresAt: exp, Expired: expired})
}

func (t *Twin) userinfo(w http.ResponseWriter, r *http.Request) {
	tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	claims, err := t.verify(tok)
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		oauthError(w, http.StatusUnauthorized, "invalid_token", err.Error())
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	// A real provider refuses an id_token here: only access tokens are
	// bearer credentials.
	if jti, _ := claims["jti"].(string); !t.access[jti] {
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		oauthError(w, http.StatusUnauthorized, "invalid_token", "not an access token")
		return
	}
	for _, u := range t.users {
		if u.Sub == claims["sub"] {
			// The current groups: a refresh reads them here (gateway/idp/core.go).
			kit.WriteJSON(w, http.StatusOK, t.claims(u, map[string]any{}))
			return
		}
	}
	w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
	oauthError(w, http.StatusUnauthorized, "invalid_token", "the subject no longer exists")
}

func (t *Twin) jwks(w http.ResponseWriter, _ *http.Request) {
	pub := t.key.PublicKey
	kit.WriteJSON(w, http.StatusOK, map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "use": "sig", "alg": "RS256", "kid": t.kid,
		"n": b64(pub.N.Bytes()), "e": b64(big.NewInt(int64(pub.E)).Bytes()),
	}}})
}

// claims adds the user claims hoop reads (gateway/idp/oidc/oidc.go
// parseUserInfo) to base. The caller holds t.mu.
func (t *Twin) claims(u User, base map[string]any) map[string]any {
	base["sub"] = u.Sub
	base["email"] = u.Email
	base["email_verified"] = true
	base["name"] = u.Name
	groups := u.Groups
	if groups == nil {
		groups = []string{}
	}
	base[t.cfg.GroupsClaim] = groups
	return base
}

func (t *Twin) sign(claims map[string]any) (string, error) {
	header, err := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": t.kid})
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	input := b64(header) + "." + b64(payload)
	sum := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, t.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}
	return input + "." + b64(sig), nil
}

// verify checks a token this twin signed and that it has not expired.
func (t *Twin) verify(tok string) (map[string]any, error) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("not a JWT")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&t.key.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
		return nil, fmt.Errorf("bad signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		return nil, err
	}
	if exp, _ := claims["exp"].(float64); time.Now().Unix() >= int64(exp) {
		return nil, fmt.Errorf("token expired")
	}
	return claims, nil
}

func oauthError(w http.ResponseWriter, status int, code, desc string) {
	w.Header().Set("Cache-Control", "no-store")
	kit.WriteJSON(w, status, map[string]string{"error": code, "error_description": desc})
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func randomString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)[:n]
}
