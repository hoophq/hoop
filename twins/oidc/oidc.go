// Package oidc is a twin of an OpenID Connect provider: discovery,
// authorize, token (authorization_code and refresh_token), userinfo and
// JWKS. Users and groups come from /_twin/seed.
//
// Authorize asks nothing: it approves the seeded user named by the standard
// login_hint parameter, so a scenario logs in over HTTP with no browser.
package oidc

import (
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
	"strings"
	"sync"
	"time"

	"github.com/hoophq/hoop/twins/kit"
)

const defaultGroupsClaim = "https://app.hoop.dev/groups"

type User struct {
	Sub    string   `json:"sub"`
	Email  string   `json:"email"`
	Name   string   `json:"name"`
	Groups []string `json:"groups"`
}

type seed struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	GroupsClaim  string `json:"groups_claim"`
	TokenTTLSec  int    `json:"token_ttl_sec"`
	Users        []User `json:"users"`
}

type code struct {
	Code        string    `json:"code"`
	Email       string    `json:"email"`
	RedirectURI string    `json:"redirect_uri"`
	IssuedAt    time.Time `json:"issued_at"`
	Used        bool      `json:"used"`
}

type issued struct {
	Kind      string    `json:"kind"` // access_token, id_token, refresh_token
	Sub       string    `json:"sub"`
	Grant     string    `json:"grant"`
	Suffix    string    `json:"suffix"` // last 8 chars, enough to match a log line
	ExpiresAt time.Time `json:"expires_at"`
}

type Twin struct {
	issuer string
	key    *rsa.PrivateKey
	kid    string

	mu      sync.Mutex
	cfg     seed
	users   map[string]User // by email
	codes   map[string]*code
	refresh map[string]string // refresh token -> email
	issued  []issued
}

func New(issuer string) (*Twin, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	t := &Twin{issuer: strings.TrimSuffix(issuer, "/"), key: key, kid: "twin-1"}
	t.Reset()
	return t, nil
}

func (t *Twin) Name() string { return "oidc" }

func (t *Twin) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cfg = seed{ClientID: "hoop", ClientSecret: "hoop-secret", GroupsClaim: defaultGroupsClaim, TokenTTLSec: 3600}
	t.users = map[string]User{}
	t.codes = map[string]*code{}
	t.refresh = map[string]string{}
	t.issued = nil
}

func (t *Twin) Seed(raw json.RawMessage) error {
	var s seed
	if err := json.Unmarshal(raw, &s); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if s.ClientID != "" {
		t.cfg.ClientID, t.cfg.ClientSecret = s.ClientID, s.ClientSecret
	}
	if s.GroupsClaim != "" {
		t.cfg.GroupsClaim = s.GroupsClaim
	}
	if s.TokenTTLSec != 0 {
		t.cfg.TokenTTLSec = s.TokenTTLSec
	}
	for _, u := range s.Users {
		if u.Email == "" || u.Sub == "" {
			return fmt.Errorf("a user needs sub and email, got %+v", u)
		}
		t.users[strings.ToLower(u.Email)] = u
	}
	return nil
}

func (t *Twin) State() any {
	t.mu.Lock()
	defer t.mu.Unlock()
	codes := make([]code, 0, len(t.codes))
	for _, c := range t.codes {
		codes = append(codes, *c)
	}
	users := make([]User, 0, len(t.users))
	for _, u := range t.users {
		users = append(users, u)
	}
	return map[string]any{"issuer": t.issuer, "client_id": t.cfg.ClientID, "groups_claim": t.cfg.GroupsClaim,
		"users": users, "codes": codes, "tokens": t.issued}
}

func (t *Twin) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /.well-known/openid-configuration", t.discovery)
	mux.HandleFunc("GET /authorize", t.authorize)
	mux.HandleFunc("POST /token", t.token)
	mux.HandleFunc("GET /userinfo", t.userinfo)
	mux.HandleFunc("GET /jwks", t.jwks)
}

func (t *Twin) discovery(w http.ResponseWriter, _ *http.Request) {
	kit.WriteJSON(w, http.StatusOK, map[string]any{
		"issuer":                                t.issuer,
		"authorization_endpoint":                t.issuer + "/authorize",
		"token_endpoint":                        t.issuer + "/token",
		"userinfo_endpoint":                     t.issuer + "/userinfo",
		"jwks_uri":                              t.issuer + "/jwks",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"scopes_supported":                      []string{"openid", "email", "profile", "offline_access"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
	})
}

func (t *Twin) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	t.mu.Lock()
	defer t.mu.Unlock()
	if q.Get("client_id") != t.cfg.ClientID {
		http.Error(w, "unknown client_id", http.StatusBadRequest)
		return
	}
	if q.Get("response_type") != "code" {
		http.Error(w, "only response_type=code", http.StatusBadRequest)
		return
	}
	u, ok := t.users[strings.ToLower(q.Get("login_hint"))]
	if !ok {
		http.Error(w, fmt.Sprintf("login_hint %q names no seeded user", q.Get("login_hint")), http.StatusBadRequest)
		return
	}
	redirect, err := url.Parse(q.Get("redirect_uri"))
	if err != nil || redirect.Host == "" {
		http.Error(w, "bad redirect_uri", http.StatusBadRequest)
		return
	}
	c := &code{Code: randomString(24), Email: u.Email, RedirectURI: redirect.String(), IssuedAt: time.Now().UTC()}
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
	if !ok {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if id != t.cfg.ClientID || secret != t.cfg.ClientSecret {
		oauthError(w, http.StatusUnauthorized, "invalid_client", "client authentication failed")
		return
	}
	var email string
	grant := r.PostForm.Get("grant_type")
	switch grant {
	case "authorization_code":
		c, ok := t.codes[r.PostForm.Get("code")]
		if !ok || c.Used || c.RedirectURI != r.PostForm.Get("redirect_uri") {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "unknown, used or mismatched code")
			return
		}
		c.Used = true
		email = c.Email
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
	u, ok := t.users[strings.ToLower(email)]
	if !ok {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "user no longer exists")
		return
	}
	ttl := time.Duration(t.cfg.TokenTTLSec) * time.Second
	exp := time.Now().Add(ttl)
	access := t.sign(map[string]any{"iss": t.issuer, "sub": u.Sub, "aud": t.cfg.ClientID,
		"client_id": t.cfg.ClientID, "scope": "openid email profile", "iat": time.Now().Unix(), "exp": exp.Unix()})
	idToken := t.sign(t.claims(u, map[string]any{"iss": t.issuer, "aud": t.cfg.ClientID, "azp": t.cfg.ClientID,
		"iat": time.Now().Unix(), "exp": exp.Unix()}))
	refresh := r.PostForm.Get("refresh_token")
	if refresh == "" {
		refresh = randomString(32)
		t.refresh[refresh] = u.Email
	}
	for _, it := range []struct{ kind, tok string }{{"access_token", access}, {"id_token", idToken}, {"refresh_token", refresh}} {
		t.issued = append(t.issued, issued{Kind: it.kind, Sub: u.Sub, Grant: grant, Suffix: it.tok[len(it.tok)-8:], ExpiresAt: exp.UTC()})
	}
	kit.WriteJSON(w, http.StatusOK, map[string]any{"access_token": access, "id_token": idToken,
		"refresh_token": refresh, "token_type": "Bearer", "expires_in": int(ttl.Seconds())})
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
	for _, u := range t.users {
		if u.Sub == claims["sub"] {
			kit.WriteJSON(w, http.StatusOK, t.claims(u, map[string]any{}))
			return
		}
	}
	oauthError(w, http.StatusUnauthorized, "invalid_token", "subject no longer exists")
}

func (t *Twin) jwks(w http.ResponseWriter, _ *http.Request) {
	pub := t.key.PublicKey
	kit.WriteJSON(w, http.StatusOK, map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "use": "sig", "alg": "RS256", "kid": t.kid,
		"n": b64(pub.N.Bytes()), "e": b64(big.NewInt(int64(pub.E)).Bytes()),
	}}})
}

// claims are the user claims hoop reads (gateway/idp/oidc/oidc.go parseUserInfo).
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

func (t *Twin) sign(claims map[string]any) string {
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": t.kid})
	payload, _ := json.Marshal(claims)
	input := b64(header) + "." + b64(payload)
	sum := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, t.key, crypto.SHA256, sum[:])
	if err != nil {
		panic(err) // a fresh 2048-bit key cannot fail to sign
	}
	return input + "." + b64(sig)
}

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
	if exp, _ := claims["exp"].(float64); time.Now().Unix() > int64(exp) {
		return nil, fmt.Errorf("token expired")
	}
	return claims, nil
}

func oauthError(w http.ResponseWriter, status int, code, desc string) {
	kit.WriteJSON(w, status, map[string]string{"error": code, "error_description": desc})
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func randomString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)[:n]
}
