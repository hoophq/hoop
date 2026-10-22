package oidc_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/twins/kit"
	"github.com/hoophq/hoop/twins/oidc"
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

// Only an access token is a bearer credential at userinfo, as at a real
// provider.
func TestUserinfoRefusesIDToken(t *testing.T) {
	s := startTwin(t)
	s.seed(t, alice)
	tok := s.login(t, "alice@example.com")
	if code := getJSON(t, s.client, s.issuer+"/userinfo", tok.IDToken, nil); code != http.StatusUnauthorized {
		t.Fatalf("userinfo with the id_token: %d, want 401", code)
	}
	if code := getJSON(t, s.client, s.issuer+"/userinfo", tok.AccessToken, nil); code != http.StatusOK {
		t.Fatalf("userinfo with the access token: %d, want 200", code)
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

// Two grants in the same second still issue distinct tokens, so the
// suffixes in /_twin/state stay distinct.
func TestTokensAreUnique(t *testing.T) {
	s := startTwin(t)
	s.seed(t, alice)
	first := s.login(t, "alice@example.com")
	var fresh tokenAnswer
	postForm(t, s.client, s.issuer+"/token", url.Values{"grant_type": {"refresh_token"},
		"refresh_token": {first.RefreshToken}, "client_id": {"hoop"}, "client_secret": {"hoop-secret"}}, &fresh)
	if fresh.AccessToken == first.AccessToken || fresh.IDToken == first.IDToken {
		t.Fatal("a refresh in the same second returned the same token")
	}
	var suffixes []string
	b, _ := json.Marshal(s.state(t)["tokens"])
	var recorded []struct{ Kind, Suffix string }
	if err := json.Unmarshal(b, &recorded); err != nil {
		t.Fatal(err)
	}
	for _, r := range recorded {
		if r.Kind != "refresh_token" {
			suffixes = append(suffixes, r.Suffix)
		}
	}
	slices.Sort(suffixes)
	if len(suffixes) != 4 || len(slices.Compact(suffixes)) != 4 {
		t.Fatalf("want 4 distinct access and id token suffixes, got %v", suffixes)
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

// With no login_hint, authorize serves a page of the seeded users. Each
// button sends the same request with login_hint, which approves.
func TestLoginPage(t *testing.T) {
	s := startTwin(t)
	s.seed(t, alice)
	s.seed(t, `{"users":[{"sub":"bob-sub","email":"bob@example.com","name":"Bob","groups":["dba"]}]}`)

	p := authorizeParams("")
	p.Del("login_hint")
	p.Set("nonce", "n-1")
	status, ctype, page := s.get(t, "/authorize?"+p.Encode())
	if status != http.StatusOK || !strings.HasPrefix(ctype, "text/html") {
		t.Fatalf("authorize with no login_hint: %d %s, want the login page", status, ctype)
	}
	for _, want := range []string{
		`<form method="get" action="` + s.issuer + `/authorize">`,
		`<input type="hidden" name="client_id" value="hoop">`,
		`<input type="hidden" name="nonce" value="n-1">`,
		`<input type="hidden" name="redirect_uri" value="` + callbackURL + `">`,
		`<input type="hidden" name="state" value="st-1">`,
		`name="login_hint" value="Alice@Example.com"`,
		`<span class="chip">admin</span><span class="chip">sre</span>`,
		`name="login_hint" value="bob@example.com"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the login page lacks %s", want)
		}
	}
	if strings.Index(page, "Alice@Example.com") > strings.Index(page, "bob@example.com") {
		t.Error("the login page must list the users by email")
	}
	if strings.Contains(page, `<input type="hidden" name="login_hint"`) {
		t.Error("the page must not carry a login_hint of its own")
	}

	// The button: the same query plus login_hint.
	p.Set("login_hint", "bob@example.com")
	code, loc := s.authorize(t, p)
	if code != http.StatusFound || loc.Query().Get("code") == "" || loc.Query().Get("state") != "st-1" {
		t.Fatalf("pick bob: got %d %v", code, loc)
	}

	// The page comes after the client and redirect checks.
	p.Del("login_hint")
	p.Set("client_id", "other")
	if code, _ := s.authorize(t, p); code != http.StatusBadRequest {
		t.Fatalf("unknown client with no login_hint: got %d, want 400", code)
	}
}

// The avatar holds the first letter of the name, or of the email when the
// user has no name.
func TestLoginPageInitials(t *testing.T) {
	s := startTwin(t)
	s.seed(t, `{"users":[{"sub":"e","email":"elodie@example.com","name":"élodie"},{"sub":"z","email":"zed@example.com"}]}`)
	p := authorizeParams("")
	p.Del("login_hint")
	_, _, page := s.get(t, "/authorize?"+p.Encode())
	for _, want := range []string{`<span class="avatar">É</span>`, `<span class="avatar">Z</span>`} {
		if !strings.Contains(page, want) {
			t.Errorf("the login page lacks %s", want)
		}
	}
}

func TestLoginPageWithNoUsers(t *testing.T) {
	s := startTwin(t)
	p := authorizeParams("")
	p.Del("login_hint")
	if status, _, page := s.get(t, "/authorize?"+p.Encode()); status != http.StatusOK || !strings.Contains(page, "No users seeded") {
		t.Fatalf("an empty twin: %d %s", status, page)
	}
}

// A browser reaches authorize at the browser URL, and the login page sends
// its form there; the gateway reaches the rest at the issuer.
func TestDiscoveryBrowserURL(t *testing.T) {
	for name, tc := range map[string]struct{ browserURL, want string }{
		"default: the issuer":          {"", "https://oidc-twin/authorize"},
		"a browser URL":                {"http://127.0.0.1:18081/", "http://127.0.0.1:18081/authorize"},
		"a browser URL behind a proxy": {"http://127.0.0.1:18081/twins/oidc", "http://127.0.0.1:18081/twins/oidc/authorize"},
	} {
		t.Run(name, func(t *testing.T) {
			twin, err := oidc.New("https://oidc-twin", tc.browserURL)
			if err != nil {
				t.Fatal(err)
			}
			srv := httptest.NewServer(kit.Handler(twin))
			defer srv.Close()
			var doc map[string]any
			if code := getJSON(t, srv.Client(), srv.URL+"/.well-known/openid-configuration", "", &doc); code != http.StatusOK {
				t.Fatalf("discovery: %d", code)
			}
			if doc["authorization_endpoint"] != tc.want {
				t.Errorf("authorization_endpoint %v, want %s", doc["authorization_endpoint"], tc.want)
			}
			p := authorizeParams("")
			p.Del("login_hint")
			resp, err := srv.Client().Get(srv.URL + "/authorize?" + p.Encode())
			if err != nil {
				t.Fatal(err)
			}
			page, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if want := `<form method="get" action="` + tc.want + `">`; !strings.Contains(string(page), want) {
				t.Errorf("the login page lacks %s", want)
			}
			for _, k := range []string{"token_endpoint", "userinfo_endpoint", "jwks_uri"} {
				if u, _ := doc[k].(string); !strings.HasPrefix(u, "https://oidc-twin/") {
					t.Errorf("%s %q: want it on the issuer", k, u)
				}
			}
		})
	}
	// authorize goes at the end of the path: a query or a fragment would
	// hide it from the request.
	for _, bad := range []string{"127.0.0.1:18081", "http://127.0.0.1:18081?mode=test",
		"http://127.0.0.1:18081/?", "http://127.0.0.1:18081/#top", "http://127.0.0.1:18081/twins#"} {
		if _, err := oidc.New("https://oidc-twin", bad); err == nil {
			t.Errorf("browser URL %q must be refused", bad)
		}
	}
}

// The login page and the state page read what /_twin/seed writes. Run with
// -race: a read outside the twin's lock is a data race.
func TestPagesWhileSeeding(t *testing.T) {
	s := startTwin(t)
	p := authorizeParams("")
	p.Del("login_hint")
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 100 {
			body := fmt.Sprintf(`{"client_id":"hoop","client_secret":"hoop-secret","users":[{"sub":"u%d","email":"u%d@example.com","name":"U%d"}]}`, i, i, i)
			resp, err := s.client.Post(s.issuer+"/_twin/seed", "application/json", strings.NewReader(body))
			if err != nil {
				t.Errorf("seed: %v", err)
				return
			}
			if err := resp.Body.Close(); err != nil {
				t.Errorf("seed: close the answer: %v", err)
				return
			}
			if resp.StatusCode != http.StatusNoContent {
				t.Errorf("seed %d: status %d", i, resp.StatusCode)
				return
			}
		}
	}()
	for range 30 {
		for _, path := range []string{"/authorize?" + p.Encode(), "/_twin/ui"} {
			if status, _, _ := s.get(t, path); status != http.StatusOK {
				t.Fatalf("GET %s while seeding: %d", path, status)
			}
		}
	}
	<-done
}

// The OIDC page of /_twin/ui shows the users with their groups, the codes
// and the tokens.
func TestStatePage(t *testing.T) {
	s := startTwin(t)
	s.seed(t, alice)
	s.login(t, "alice@example.com")
	status, _, page := s.get(t, "/_twin/ui")
	if status != http.StatusOK {
		t.Fatalf("/_twin/ui: %d", status)
	}
	for _, want := range []string{
		`<section data-section="users"><h2>Users <span class="count">1</span></h2>`,
		`<td>Alice@Example.com</td><td>Alice</td><td><span class="chip">admin</span><span class="chip">sre</span></td>`,
		`<section data-section="codes"><h2>Codes <span class="count">1</span></h2>`,
		`<section data-section="tokens"><h2>Tokens <span class="count">3</span></h2>`,
		`<td>access_token</td><td>alice-sub</td><td>authorization_code</td>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the state page lacks %s", want)
		}
	}
	users, codes, tokens := strings.Index(page, `data-section="users"`), strings.Index(page, `data-section="codes"`), strings.Index(page, `data-section="tokens"`)
	if users > codes || codes > tokens {
		t.Error("the state page must show users, then codes, then tokens")
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
