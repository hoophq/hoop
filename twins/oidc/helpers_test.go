package oidc_test

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/hoophq/hoop/twins/kit"
	"github.com/hoophq/hoop/twins/oidc"
)

const callbackURL = "http://127.0.0.1:3587/callback"

// twinServer is an in-process OIDC twin over TLS, as the gateway reaches it.
type twinServer struct {
	issuer string
	client *http.Client // trusts the server's certificate, follows no redirect
}

func startTwin(t *testing.T) *twinServer {
	t.Helper()
	srv := httptest.NewUnstartedServer(nil)
	issuer := "https://" + srv.Listener.Addr().String()
	twin, err := oidc.New(issuer)
	if err != nil {
		t.Fatal(err)
	}
	srv.Config.Handler = kit.Handler(twin)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	client := srv.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &twinServer{issuer: issuer, client: client}
}

func (s *twinServer) seed(t *testing.T, body string) {
	t.Helper()
	resp, err := s.client.Post(s.issuer+"/_twin/seed", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("seed %s: %d %s", body, resp.StatusCode, b)
	}
}

func (s *twinServer) state(t *testing.T) map[string]any {
	t.Helper()
	var st struct {
		State map[string]any `json:"state"`
	}
	if code := getJSON(t, s.client, s.issuer+"/_twin/state", "", &st); code != http.StatusOK {
		t.Fatalf("state: %d", code)
	}
	return st.State
}

// authorize runs GET /authorize and returns the redirect it answers with.
func (s *twinServer) authorize(t *testing.T, params url.Values) (int, *url.URL) {
	t.Helper()
	resp, err := s.client.Get(s.issuer + "/authorize?" + params.Encode())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	return resp.StatusCode, loc
}

func authorizeParams(loginHint string) url.Values {
	return url.Values{"client_id": {"hoop"}, "response_type": {"code"}, "scope": {"openid profile email"},
		"redirect_uri": {callbackURL}, "state": {"st-1"}, "login_hint": {loginHint}}
}

// login runs the code flow for a seeded user and returns the token answer.
func (s *twinServer) login(t *testing.T, email string) tokenAnswer {
	t.Helper()
	code, loc := s.authorize(t, authorizeParams(email))
	if code != http.StatusFound || loc.Query().Get("code") == "" {
		t.Fatalf("authorize %s: %d %v", email, code, loc)
	}
	var tok tokenAnswer
	status := postForm(t, s.client, s.issuer+"/token", url.Values{"grant_type": {"authorization_code"},
		"code": {loc.Query().Get("code")}, "redirect_uri": {callbackURL},
		"client_id": {"hoop"}, "client_secret": {"hoop-secret"}}, &tok)
	if status != http.StatusOK {
		t.Fatalf("code exchange %s: %d %+v", email, status, tok)
	}
	return tok
}

type tokenAnswer struct {
	AccessToken  string `json:"access_token"`
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Error        string `json:"error"`
}

func getJSON(t *testing.T, c *http.Client, target, bearer string, out any) int {
	t.Helper()
	req, _ := http.NewRequest("GET", target, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	defer resp.Body.Close()
	decode(t, resp, out)
	return resp.StatusCode
}

func postForm(t *testing.T, c *http.Client, target string, form url.Values, out any) int {
	t.Helper()
	resp, err := c.PostForm(target, form)
	if err != nil {
		t.Fatalf("POST %s: %v", target, err)
	}
	defer resp.Body.Close()
	decode(t, resp, out)
	return resp.StatusCode
}

func decode(t *testing.T, resp *http.Response, out any) {
	t.Helper()
	b, _ := io.ReadAll(resp.Body)
	if out == nil || len(b) == 0 {
		return
	}
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatalf("%s %s: %d, body is not JSON: %q", resp.Request.Method, resp.Request.URL, resp.StatusCode, b)
	}
}

// jwtClaims decodes the payload of a JWT. It does not check the signature.
func jwtClaims(t *testing.T, tok string) map[string]any {
	t.Helper()
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("not a JWT: %q", tok)
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatal(err)
	}
	return claims
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// verifyRS256 checks a JWT signature against the JWKS key its kid names.
func verifyRS256(tok string, keys []jwk) error {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return fmt.Errorf("not a JWT")
	}
	hb, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return err
	}
	var header struct{ Alg, Kid string }
	if err := json.Unmarshal(hb, &header); err != nil {
		return err
	}
	if header.Alg != "RS256" {
		return fmt.Errorf("alg %q, want RS256", header.Alg)
	}
	for _, k := range keys {
		if k.Kid != header.Kid || k.Kty != "RSA" {
			continue
		}
		n, err1 := base64.RawURLEncoding.DecodeString(k.N)
		e, err2 := base64.RawURLEncoding.DecodeString(k.E)
		if err1 != nil || err2 != nil {
			return fmt.Errorf("key %s: bad n or e", k.Kid)
		}
		pub := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
		sig, err := base64.RawURLEncoding.DecodeString(parts[2])
		if err != nil {
			return err
		}
		sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		return rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig)
	}
	return fmt.Errorf("no RSA key with kid %q in the JWKS", header.Kid)
}
