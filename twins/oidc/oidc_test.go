package oidc_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

const alice = `{"users":[{"sub":"alice-sub","email":"Alice@Example.com","name":"Alice","groups":["admin","sre"]}]}`

func TestAuthorize(t *testing.T) {
	s := startTwin(t)
	s.seed(t, alice)

	p := authorizeParams("alice@example.com")
	p.Set("nonce", "n-1")
	code, loc := s.authorize(t, p)
	if code != http.StatusFound || loc.Query().Get("code") == "" || loc.Query().Get("state") != "st-1" ||
		loc.Scheme+"://"+loc.Host+loc.Path != callbackURL {
		t.Fatalf("approve with no prompt: got %d %v", code, loc)
	}

	for name, tc := range map[string]struct {
		set       url.Values
		wantCode  int
		wantError string
	}{
		"unknown client":    {url.Values{"client_id": {"other"}}, http.StatusBadRequest, ""},
		"relative redirect": {url.Values{"redirect_uri": {"/callback"}}, http.StatusBadRequest, ""},
		"unknown user":      {url.Values{"login_hint": {"mallory@example.com"}}, http.StatusBadRequest, ""},
		"no openid scope":   {url.Values{"scope": {"profile email"}}, http.StatusFound, "invalid_scope"},
		"implicit flow":     {url.Values{"response_type": {"token"}}, http.StatusFound, "unsupported_response_type"},
	} {
		t.Run(name, func(t *testing.T) {
			p := authorizeParams("alice@example.com")
			for k, v := range tc.set {
				p[k] = v
			}
			code, loc := s.authorize(t, p)
			if code != tc.wantCode || loc.Query().Get("error") != tc.wantError || loc.Query().Get("code") != "" {
				t.Fatalf("got %d %v, want %d error=%q and no code", code, loc, tc.wantCode, tc.wantError)
			}
		})
	}
}

func TestTokenExchange(t *testing.T) {
	s := startTwin(t)
	s.seed(t, alice)
	p := authorizeParams("alice@example.com")
	p.Set("nonce", "n-1")
	_, loc := s.authorize(t, p)
	form := url.Values{"grant_type": {"authorization_code"}, "code": {loc.Query().Get("code")}, "redirect_uri": {callbackURL}}

	// Basic auth with form-encoded credentials, as golang.org/x/oauth2 sends it.
	req, _ := http.NewRequest("POST", s.issuer+"/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(url.QueryEscape("hoop"), url.QueryEscape("hoop-secret"))
	resp, err := s.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var tok tokenAnswer
	decode(t, resp, &tok)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || tok.IDToken == "" || tok.RefreshToken == "" {
		t.Fatalf("code exchange: %d %+v", resp.StatusCode, tok)
	}
	id := jwtClaims(t, tok.IDToken)
	if id["sub"] != "alice-sub" || id["aud"] != "hoop" || id["azp"] != "hoop" || id["nonce"] != "n-1" ||
		id["email"] != "Alice@Example.com" || !hasGroups(id["https://app.hoop.dev/groups"], "admin", "sre") {
		t.Fatalf("id_token claims: %v", id)
	}

	form.Set("client_id", "hoop")
	form.Set("client_secret", "hoop-secret")
	var again tokenAnswer
	if code := postForm(t, s.client, s.issuer+"/token", form, &again); code != http.StatusBadRequest || again.Error != "invalid_grant" {
		t.Fatalf("a used code: got %d %+v, want 400 invalid_grant", code, again)
	}
}

// The twin moves a user out of a group; the next refresh and userinfo show it.
func TestRefreshSeesGroupChange(t *testing.T) {
	s := startTwin(t)
	s.seed(t, alice)
	tok := s.login(t, "alice@example.com")
	s.seed(t, `{"users":[{"sub":"alice-sub","email":"alice@example.com","name":"Alice","groups":["sre"]}]}`)

	var fresh tokenAnswer
	if code := postForm(t, s.client, s.issuer+"/token", url.Values{"grant_type": {"refresh_token"},
		"refresh_token": {tok.RefreshToken}, "client_id": {"hoop"}, "client_secret": {"hoop-secret"}}, &fresh); code != http.StatusOK {
		t.Fatalf("refresh: %d %+v", code, fresh)
	}
	var info map[string]any
	getJSON(t, s.client, s.issuer+"/userinfo", fresh.AccessToken, &info)
	if !hasGroups(info["https://app.hoop.dev/groups"], "sre") {
		t.Fatalf("userinfo after the group change: %v", info)
	}
}

// Failure injection specific to OIDC: tokens issued already expired.
func TestExpiredTokens(t *testing.T) {
	s := startTwin(t)
	s.seed(t, alice)
	s.seed(t, `{"expired_tokens":["id_token"]}`)
	tok := s.login(t, "alice@example.com")
	now := float64(time.Now().Unix())
	if exp := jwtClaims(t, tok.IDToken)["exp"].(float64); exp >= now {
		t.Fatalf("id_token exp %v, want in the past", exp)
	}
	if exp := jwtClaims(t, tok.AccessToken)["exp"].(float64); exp <= now {
		t.Fatalf("access_token exp %v, want in the future: only id_token was asked", exp)
	}
	var recorded []struct {
		Kind, Sub, Grant string
		Expired          bool
	}
	b, _ := json.Marshal(s.state(t)["tokens"])
	if err := json.Unmarshal(b, &recorded); err != nil {
		t.Fatal(err)
	}
	if len(recorded) != 3 || recorded[1].Kind != "id_token" || !recorded[1].Expired || recorded[0].Expired {
		t.Fatalf("state must record the id_token as expired, and only it: %+v", recorded)
	}

	s.seed(t, `{"expired_tokens":["access_token"]}`)
	tok = s.login(t, "alice@example.com")
	if code := getJSON(t, s.client, s.issuer+"/userinfo", tok.AccessToken, nil); code != http.StatusUnauthorized {
		t.Fatalf("userinfo with an expired access token: %d, want 401", code)
	}

	s.seed(t, `{"expired_tokens":[]}`)
	tok = s.login(t, "alice@example.com")
	if exp := jwtClaims(t, tok.IDToken)["exp"].(float64); exp <= now {
		t.Fatal("an empty expired_tokens must stop the expired tokens")
	}
}

func TestGroupsClaimName(t *testing.T) {
	s := startTwin(t)
	s.seed(t, `{"groups_claim":"groups"}`)
	s.seed(t, alice)
	tok := s.login(t, "alice@example.com")
	if id := jwtClaims(t, tok.IDToken); !hasGroups(id["groups"], "admin", "sre") || id["https://app.hoop.dev/groups"] != nil {
		t.Fatalf("id_token must carry the groups in the seeded claim only: %v", id)
	}
}

func TestSeedRefusesBadInput(t *testing.T) {
	s := startTwin(t)
	s.seed(t, alice)
	for _, body := range []string{
		`{"users":[{"email":"no-sub@example.com"}]}`,
		`{"users":[{"sub":"alice-sub","email":"other@example.com"}]}`,
		`{"users":[{"sub":"x","email":"x@example.com"},{"sub":"x","email":"y@example.com"}]}`,
		`{"expired_tokens":["refresh_token"]}`,
		`{"token_ttl_sec":-1}`,
		`{"client_id":"only-the-id"}`,
		`{"grups_claim":"typo"}`,
	} {
		resp, err := s.client.Post(s.issuer+"/_twin/seed", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("seed %s: got %d, want 400", body, resp.StatusCode)
		}
	}
}

func TestStateAndReset(t *testing.T) {
	s := startTwin(t)
	s.seed(t, alice)
	s.login(t, "alice@example.com")
	st := s.state(t)
	if codes, _ := st["codes"].([]any); len(codes) != 1 {
		t.Fatalf("state codes: %v", st["codes"])
	}
	if tokens, _ := st["tokens"].([]any); len(tokens) != 3 {
		t.Fatalf("state tokens: want access, id and refresh, got %v", st["tokens"])
	}

	resp, err := s.client.Post(s.issuer+"/_twin/reset", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	st = s.state(t)
	if codes, _ := st["codes"].([]any); len(codes) != 0 {
		t.Fatalf("reset must clear the codes: %v", st["codes"])
	}
	if tokens, _ := st["tokens"].([]any); len(tokens) != 0 {
		t.Fatalf("reset must clear the tokens: %v", st["tokens"])
	}
	if code, _ := s.authorize(t, authorizeParams("alice@example.com")); code != http.StatusBadRequest {
		t.Fatalf("reset must clear the users, authorize got %d", code)
	}
}

func hasGroups(claim any, want ...string) bool {
	list, ok := claim.([]any)
	if !ok || len(list) != len(want) {
		return false
	}
	for i, g := range want {
		if list[i] != g {
			return false
		}
	}
	return true
}
