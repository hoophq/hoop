//go:build scenario

package scenario

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
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
		{"sub": "bob-sub", "email": "bob@example.com", "name": "Bob", "groups": []string{"admin", "sre"}},
		{"sub": "carol-sub", "email": "carol@example.com", "name": "Carol", "groups": []string{"sre"}},
		{"sub": "dave-sub", "email": "dave@example.com", "name": "Dave", "groups": []string{"admin", "dba"}},
		{"sub": "erin-sub", "email": "erin@example.com", "name": "Erin", "groups": []string{"sre"}},
	}})
	s.mark(t, "OIDC twin seeded")

	t.Run("the first OIDC user is created as admin", func(t *testing.T) {
		// The stack logged alice in at boot, on an empty database. The IdP
		// has her in group sre only.
		me := userinfo(t, s.admin)
		if me.Email != firstUserEmail || !me.IsAdmin || !slices.Contains(me.Groups, "admin") {
			t.Fatalf("alice is the first user: want admin, got %+v", me)
		}
		var users []struct{ Email string }
		must(t, "GET", apiURL+"/api/users", s.admin, nil, &users)
		if len(users) != 1 {
			t.Fatalf("alice must be the only user of a new org, got %+v: does the stack reuse a database?", users)
		}
		s.mark(t, "alice, the first user, is admin")
	})

	t.Run("a user in group admin is admin and a user without it is not", func(t *testing.T) {
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
		// Short-lived tokens, so the next API call refreshes.
		seedOIDC(t, map[string]any{"token_ttl_sec": 3})
		t.Cleanup(func() { seedOIDC(t, map[string]any{"token_ttl_sec": 3600}) })
		dave := s.login(t, "dave@example.com")
		if me := userinfo(t, dave); !me.IsAdmin {
			t.Fatalf("dave is in group admin: want admin before the change, got %+v", me)
		}

		// The IdP removes dave from admin and adds him to oncall. Only a group
		// sync can bring oncall into hoop.
		seedOIDC(t, map[string]any{"users": []map[string]any{
			{"sub": "dave-sub", "email": "dave@example.com", "name": "Dave", "groups": []string{"dba", "oncall"}}}})
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
		if !slices.Contains(me.Groups, "oncall") {
			t.Fatalf("the refresh did not sync the groups from userinfo: got %+v", me)
		}
		// Today: the sync adds admin back (gateway/idp/core.go
		// syncUserGroupsFromUserInfo), so dave has [dba oncall admin].
		groups := slices.Sorted(slices.Values(me.Groups))
		if !me.IsAdmin || !slices.Equal(groups, []string{"admin", "dba", "oncall"}) {
			t.Fatalf("%s looks fixed: after the refresh dave is %+v. Assert he is not admin, and drop bugStickyAdmin", bugStickyAdmin, me)
		}
		t.Logf("%s: dave left group admin at the IdP and is still admin in hoop: %+v", bugStickyAdmin, me)
		s.mark(t, "refresh synced dave's groups; admin stays ("+bugStickyAdmin+")")
	})

	t.Run("JWKS answers 5xx: the login fails at /api/login and the gateway keeps serving", func(t *testing.T) {
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
		if me := userinfo(t, s.admin); !me.IsAdmin {
			t.Fatalf("a signed-in admin must keep working, got %+v", me)
		}
		s.mark(t, "JWKS 503: /api/login 500, the gateway keeps serving")

		must(t, "DELETE", oidcAdmin+"/_twin/faults", "", nil, nil)
		userinfo(t, s.login(t, firstUserEmail))
		s.mark(t, "JWKS back: login works with no restart")
	})

	t.Run("an expired id_token: /api/callback refuses the login", func(t *testing.T) {
		seedOIDC(t, map[string]any{"expired_tokens": []string{"id_token"}})
		t.Cleanup(func() { seedOIDC(t, map[string]any{"expired_tokens": []string{}}) })

		final := loginFlow(t, "erin@example.com")
		if final.Query().Get("token") != "" || final.Query().Get("error") == "" {
			t.Fatalf("the callback must refuse the login: it redirected to %s", final.Redacted())
		}
		if resp := call(t, "GET", apiURL+"/api/users/erin@example.com", s.admin, nil, nil); resp.StatusCode != http.StatusNotFound {
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
