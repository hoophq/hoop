//go:build scenario

package scenario

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

// bugStickyAdmin: a refresh does not remove admin when the IdP removes the
// user from group admin. The scenario asserts today's behavior and fails
// when the fix lands.
const bugStickyAdmin = "ENG-742"

// TestOIDCLogin logs in through the OIDC twin with seeded users and groups,
// and no IdP account. The subtests are the scenarios; they share one stack
// and run in order.
func TestOIDCLogin(t *testing.T) {
	s := newStack(t)
	seedOIDC(t, map[string]any{"users": []map[string]any{
		// alice is not in group admin: only the first-user rule makes her admin.
		{"sub": "alice-sub", "email": "alice@example.com", "name": "Alice", "groups": []string{"sre"}},
		{"sub": "bob-sub", "email": "bob@example.com", "name": "Bob", "groups": []string{"admin", "sre"}},
		{"sub": "carol-sub", "email": "carol@example.com", "name": "Carol", "groups": []string{"sre"}},
		{"sub": "dave-sub", "email": "dave@example.com", "name": "Dave", "groups": []string{"admin", "dba"}},
		{"sub": "erin-sub", "email": "erin@example.com", "name": "Erin", "groups": []string{"sre"}},
	}})
	s.mark(t, "OIDC twin seeded")

	t.Run("the first OIDC user is created as admin", func(t *testing.T) {
		if s.logins > 0 {
			t.Fatal("this scenario needs a stack where nobody logged in yet")
		}
		me := userinfo(t, s.login(t, "alice@example.com"))
		if !me.IsAdmin || !slices.Contains(me.Groups, "admin") {
			t.Fatalf("alice is the first user: want admin, got %+v", me)
		}
		s.mark(t, "alice, the first user, is admin")
	})

	t.Run("a user in group admin is admin and a user without it is not", func(t *testing.T) {
		s.firstUser(t)
		bob := userinfo(t, s.login(t, "bob@example.com"))
		if !bob.IsAdmin || !slices.Contains(bob.Groups, "sre") {
			t.Fatalf("bob is in groups admin and sre: want admin, got %+v", bob)
		}
		carol := userinfo(t, s.login(t, "carol@example.com"))
		if carol.IsAdmin || !slices.Equal(carol.Groups, []string{"sre"}) {
			t.Fatalf("carol is in group sre only: want not admin, got %+v", carol)
		}
		s.mark(t, "bob (group admin) is admin, carol (sre) is not")
	})

	t.Run("the twin removes a user from admin and the next refresh syncs the groups", func(t *testing.T) {
		s.firstUser(t)
		// Short-lived tokens, so the next API call refreshes.
		seedOIDC(t, map[string]any{"token_ttl_sec": 3})
		t.Cleanup(func() { seedOIDC(t, map[string]any{"token_ttl_sec": 3600}) })
		dave := s.login(t, "dave@example.com")
		if me := userinfo(t, dave); !me.IsAdmin {
			t.Fatalf("dave is in group admin: want admin before the change, got %+v", me)
		}

		seedOIDC(t, map[string]any{"users": []map[string]any{
			{"sub": "dave-sub", "email": "dave@example.com", "name": "Dave", "groups": []string{"dba"}}}})
		time.Sleep(time.Until(jwtExpiry(t, dave)) + time.Second)

		// The expired token makes the gateway refresh, then sync the groups
		// from userinfo (gateway/idp/core.go refreshForSubject).
		var me userInfo
		resp := must(t, "GET", apiURL+"/api/userinfo", dave, nil, &me)
		if resp.Header.Get("X-New-Access-Token") == "" {
			t.Fatalf("the gateway did not refresh the expired token, got %+v", me)
		}
		if !hasRefresh(t, "dave-sub") {
			t.Fatal("the twin saw no refresh_token grant for dave")
		}
		if !slices.Contains(me.Groups, "dba") {
			t.Fatalf("the refresh did not sync the groups from userinfo: got %+v", me)
		}
		// Today: hoop keeps admin (gateway/idp/core.go syncUserGroupsFromUserInfo).
		if !me.IsAdmin {
			t.Fatalf("%s looks fixed: dave is no longer admin after the refresh (%+v). Assert that, and drop bugStickyAdmin", bugStickyAdmin, me)
		}
		t.Logf("%s: dave left group admin at the IdP and is still admin in hoop: %+v", bugStickyAdmin, me)
		s.mark(t, "refresh synced dave's groups; admin stays ("+bugStickyAdmin+")")
	})

	t.Run("JWKS answers 5xx: the login fails at /api/login and the gateway keeps serving", func(t *testing.T) {
		admin := s.firstUser(t)
		// The login handler loads the provider and its JWKS per request
		// (gateway/idp/oidc/oidc.go New); signed-in calls use a cached one.
		must(t, "POST", oidcAdmin+"/_twin/faults", "", map[string]any{"path": "/jwks", "status": 503}, nil)
		t.Cleanup(func() { must(t, "DELETE", oidcAdmin+"/_twin/faults", "", nil, nil) })

		resp := call(t, "GET", apiURL+"/api/login", "", nil, nil)
		if resp.StatusCode != http.StatusInternalServerError {
			t.Fatalf("/api/login with a failing JWKS: status %d, want 500", resp.StatusCode)
		}
		if faultHits(t) == 0 {
			t.Fatal("the gateway did not call the JWKS, so the fault proves nothing")
		}
		must(t, "GET", apiURL+"/api/healthz", "", nil, nil)
		if me := userinfo(t, admin); !me.IsAdmin {
			t.Fatalf("a signed-in admin must keep working, got %+v", me)
		}
		s.mark(t, "JWKS 503: /api/login 500, the gateway keeps serving")

		must(t, "DELETE", oidcAdmin+"/_twin/faults", "", nil, nil)
		userinfo(t, s.login(t, "alice@example.com"))
		s.mark(t, "JWKS back: login works with no restart")
	})

	t.Run("an expired id_token: /api/callback refuses the login", func(t *testing.T) {
		admin := s.firstUser(t)
		seedOIDC(t, map[string]any{"expired_tokens": []string{"id_token"}})
		t.Cleanup(func() { seedOIDC(t, map[string]any{"expired_tokens": []string{}}) })

		final := loginFlow(t, "erin@example.com")
		if final.Query().Get("token") != "" || final.Query().Get("error") == "" {
			t.Fatalf("the callback must refuse the login: it redirected to %s", final.Redacted())
		}
		if resp := call(t, "GET", apiURL+"/api/users/erin@example.com", admin, nil, nil); resp.StatusCode != http.StatusNotFound {
			t.Fatalf("erin must not exist after a refused login: status %d", resp.StatusCode)
		}
		s.mark(t, "expired id_token: /api/callback refused erin, no user created")
	})
}

type userInfo struct {
	Email   string   `json:"email"`
	IsAdmin bool     `json:"is_admin"`
	Groups  []string `json:"groups"`
}

func userinfo(t testing.TB, token string) userInfo {
	t.Helper()
	var me userInfo
	must(t, "GET", apiURL+"/api/userinfo", token, nil, &me)
	return me
}

func seedOIDC(t testing.TB, body map[string]any) {
	t.Helper()
	must(t, "POST", oidcAdmin+"/_twin/seed", "", body, nil)
}

// login runs the code flow and returns the access token the gateway hands
// to the CLI. The first user to log in is the stack's admin.
func (s *stack) login(t testing.TB, email string) string {
	t.Helper()
	final := loginFlow(t, email)
	tok := final.Query().Get("token")
	if tok == "" {
		t.Fatalf("login %s: the gateway redirected to %s", email, final.Redacted())
	}
	if s.logins == 0 {
		s.admin = tok
	}
	s.logins++
	return tok
}

// firstUser returns the first user's token. When nobody has logged in, it
// logs alice in first, so a scenario run alone does not take her place.
func (s *stack) firstUser(t testing.TB) string {
	t.Helper()
	if s.logins == 0 {
		s.login(t, "alice@example.com")
	}
	return s.admin
}

// loginFlow runs the OIDC code flow over HTTP, as a browser does for
// `hoop login`, and returns where the gateway's callback redirects.
func loginFlow(t testing.TB, email string) *url.URL {
	t.Helper()
	var lr struct {
		URL string `json:"login_url"`
	}
	must(t, "GET", apiURL+"/api/login", "", nil, &lr)
	authz, err := url.Parse(lr.URL)
	if err != nil || authz.Host != strings.TrimPrefix(issuer, "https://") {
		t.Fatalf("login_url %q: want the twin's authorize endpoint", lr.URL)
	}
	q := authz.Query()
	q.Set("login_hint", email)
	// No DNS resolves the issuer host here. The twin serves the same
	// handler on its admin port.
	admin, _ := url.Parse(oidcAdmin)
	authz.Scheme, authz.Host, authz.RawQuery = admin.Scheme, admin.Host, q.Encode()

	callback := redirect(t, authz.String(), http.StatusFound)
	final, err := url.Parse(redirect(t, callback, http.StatusTemporaryRedirect))
	if err != nil {
		t.Fatal(err)
	}
	return final
}

func redirect(t testing.TB, target string, want int) string {
	t.Helper()
	resp := call(t, "GET", target, "", nil, nil)
	if resp.StatusCode != want {
		t.Fatalf("GET %s: want %d, got %d", target, want, resp.StatusCode)
	}
	return resp.Header.Get("Location")
}

// jwtExpiry reads exp from a JWT the twin signed. It checks no signature.
func jwtExpiry(t testing.TB, tok string) time.Time {
	t.Helper()
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("not a JWT: %q", tok)
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatal(err)
	}
	return time.Unix(claims.Exp, 0)
}

type oidcState struct {
	State struct {
		Tokens []struct {
			Kind  string `json:"kind"`
			Sub   string `json:"sub"`
			Grant string `json:"grant"`
		} `json:"tokens"`
	} `json:"state"`
	Faults []struct {
		Path string `json:"path"`
		Hits int    `json:"hits"`
	} `json:"faults"`
}

func readOIDCState(t testing.TB) oidcState {
	t.Helper()
	var st oidcState
	must(t, "GET", oidcAdmin+"/_twin/state", "", nil, &st)
	return st
}

func hasRefresh(t testing.TB, sub string) bool {
	return slices.ContainsFunc(readOIDCState(t).State.Tokens, func(tk struct {
		Kind  string `json:"kind"`
		Sub   string `json:"sub"`
		Grant string `json:"grant"`
	}) bool {
		return tk.Sub == sub && tk.Grant == "refresh_token" && tk.Kind == "access_token"
	})
}

func faultHits(t testing.TB) int {
	n := 0
	for _, f := range readOIDCState(t).Faults {
		n += f.Hits
	}
	return n
}
