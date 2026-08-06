//go:build integration

package integration

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/integration/testutil"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
)

// fakeMCPServer is a remote MCP server plus the authorization server that
// protects it, standing in for something like https://mcp.linear.app/mcp.
//
// It publishes RFC 9728 protected-resource metadata pointing at itself as the
// authorization server, RFC 8414 metadata with a registration endpoint, and a
// token endpoint that mints short-lived access tokens against a rotating
// refresh token. That last part is what makes the test worth running: a
// provider that rotates refresh tokens punishes exactly the concurrency bug
// the grant row lock exists to prevent.
type fakeMCPServer struct {
	srv *httptest.Server

	mu sync.Mutex
	// issued counts access tokens minted, so a test can assert a refresh
	// happened (or did not).
	issued int
	// refreshCalls counts refresh-token grants specifically.
	refreshCalls int
	// currentRefresh is the only refresh token the server will accept. Each
	// grant rotates it, so replaying a stale one fails as invalid_grant —
	// unless keepRefreshToken is set.
	currentRefresh string
	// keepRefreshToken makes the refresh-token grant behave like the many
	// providers that do not rotate: the response omits refresh_token
	// entirely (RFC 6749 §6 makes it OPTIONAL) and the original stays valid
	// forever. A client that reads the omission as "the token is gone" can
	// never refresh this grant again.
	keepRefreshToken bool
	// revokeRefresh makes every refresh-token grant answer invalid_grant, the
	// way a provider does once the user revokes the app or the refresh token
	// is expired past recovery. Nothing the gateway holds can renew such a
	// grant, so the row is dead the moment this is seen.
	revokeRefresh bool
	// spentCodes records redeemed authorization codes so the fake enforces
	// RFC 6749 §4.1.2 single use, the way every real provider does.
	spentCodes map[string]bool
	// initialTTL is the lifetime of the token the login mints, and renewedTTL
	// the lifetime of every token a refresh mints. They differ so a test can
	// stage a grant that must be renewed on first use and then stays valid,
	// which is what a real provider looks like a few TTLs into a connection's
	// life.
	initialTTL time.Duration
	renewedTTL time.Duration
	// registered records the redirect URIs handed to dynamic registration.
	registeredRedirects []string
}

func newFakeMCPServer(t *testing.T, initialTTL, renewedTTL time.Duration) *fakeMCPServer {
	t.Helper()
	f := &fakeMCPServer{
		initialTTL:     initialTTL,
		renewedTTL:     renewedTTL,
		currentRefresh: "refresh-0",
		spentCodes:     map[string]bool{},
	}
	mux := http.NewServeMux()

	// RFC 9728: the MCP endpoint's protected-resource metadata. Hoop looks for
	// the path-suffixed form first.
	prm := func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"resource":              f.url("/mcp"),
			"authorization_servers": []string{f.url("")},
			"scopes_supported":      []string{"read", "write"},
		})
	}
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", prm)
	mux.HandleFunc("/.well-known/oauth-protected-resource", prm)

	// RFC 8414: authorization server metadata.
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":                                f.url(""),
			"authorization_endpoint":                f.url("/authorize"),
			"token_endpoint":                        f.url("/token"),
			"registration_endpoint":                 f.url("/register"),
			"scopes_supported":                      []string{"read", "write"},
			"code_challenge_methods_supported":      []string{"S256"},
			"token_endpoint_auth_methods_supported": []string{"none"},
		})
	})

	// RFC 7591 dynamic client registration.
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			RedirectURIs []string `json:"redirect_uris"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.registeredRedirects = append(f.registeredRedirects, body.RedirectURIs...)
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, map[string]any{
			"client_id":                  "client-abc",
			"token_endpoint_auth_method": "none",
		})
	})

	mux.HandleFunc("/token", f.handleToken)

	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeMCPServer) url(path string) string { return f.srv.URL + path }

// handleToken implements the authorization-code and refresh-token grants. The
// refresh token rotates on every use and the previous one is rejected, which
// is the behavior that turns a concurrent double-refresh into a permanently
// dead grant if the caller does not serialize.
func (f *fakeMCPServer) handleToken(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	f.mu.Lock()
	defer f.mu.Unlock()

	ttl := f.renewedTTL
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		if r.Form.Get("code_verifier") == "" {
			writeTokenError(w, http.StatusBadRequest, "invalid_request", "missing code_verifier")
			return
		}
		// RFC 6749 §4.1.2 requires an authorization code to be single use.
		// A real provider refuses the second redemption, which is what makes
		// a replayed callback fail its exchange — and what used to flip a
		// good flow to error.
		if code := r.Form.Get("code"); f.spentCodes[code] {
			writeTokenError(w, http.StatusBadRequest, "invalid_grant", "authorization code was already redeemed")
			return
		} else {
			f.spentCodes[code] = true
		}
		ttl = f.initialTTL
	case "refresh_token":
		f.refreshCalls++
		if f.revokeRefresh {
			writeTokenError(w, http.StatusBadRequest, "invalid_grant", "the grant was revoked")
			return
		}
		if r.Form.Get("refresh_token") != f.currentRefresh {
			writeTokenError(w, http.StatusBadRequest, "invalid_grant", "refresh token was already used")
			return
		}
	default:
		writeTokenError(w, http.StatusBadRequest, "unsupported_grant_type", "")
		return
	}

	f.issued++
	body := map[string]any{
		"access_token": fmt.Sprintf("access-%d", f.issued),
		"token_type":   "Bearer",
		"expires_in":   int(ttl.Seconds()),
	}
	// A non-rotating server omits refresh_token on renewal only; the
	// authorization-code grant always hands one out or there is nothing to
	// renew with.
	if !f.keepRefreshToken || r.Form.Get("grant_type") == "authorization_code" {
		f.currentRefresh = fmt.Sprintf("refresh-%d", f.issued)
		body["refresh_token"] = f.currentRefresh
	}
	writeJSON(w, body)
}

func (f *fakeMCPServer) counters() (issued, refreshes int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.issued, f.refreshCalls
}

func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func writeTokenError(w http.ResponseWriter, status int, code, desc string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	writeJSON(w, map[string]any{"error": code, "error_description": desc})
}

// completeMCPLogin drives the full three-hop OAuth login against the fake
// server through the real gateway handlers and returns the flow id plus the
// authorization header the create page received. The client is registered
// dynamically, which the fake registers as a public one (auth method "none").
func completeMCPLogin(t *testing.T, token string, fake *fakeMCPServer) (flowID, authHeader string) {
	t.Helper()
	return completeMCPLoginAs(t, token, fake, "", "")
}

// completeMCPLoginAs is completeMCPLogin with explicit client credentials.
// Supplying both records token_auth_method=client_secret_post on the flow, and
// therefore on the grant — the branch where Hoop builds the token request
// itself instead of delegating to the oauth library.
func completeMCPLoginAs(t *testing.T, token string, fake *fakeMCPServer, clientID, clientSecret string) (flowID, authHeader string) {
	t.Helper()

	authorize := testServer.Post(t, "/mcp-oauth/authorize", token, openapi.MCPOAuthAuthorizeRequest{
		ServerURL:    fake.url("/mcp"),
		ClientID:     clientID,
		ClientSecret: clientSecret,
	})
	defer authorize.Body.Close()
	testutil.RequireStatus(t, authorize, http.StatusOK)
	var authorizeResp openapi.MCPOAuthAuthorizeResponse
	testutil.DecodeJSON(t, authorize, &authorizeResp)
	if authorizeResp.FlowID == "" || authorizeResp.AuthorizationURL == "" {
		t.Fatalf("authorize returned an empty flow: %+v", authorizeResp)
	}

	// The authorization URL must carry the PKCE challenge and the RFC 8707
	// resource indicator; a provider that receives neither would issue a token
	// usable anywhere.
	parsed, err := url.Parse(authorizeResp.AuthorizationURL)
	if err != nil {
		t.Fatalf("authorization url is not parseable: %v", err)
	}
	q := parsed.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		t.Errorf("authorization url is missing the S256 PKCE challenge: %s", parsed.RawQuery)
	}
	if q.Get("resource") != fake.url("/mcp") {
		t.Errorf("authorization url resource = %q, want %q", q.Get("resource"), fake.url("/mcp"))
	}
	if q.Get("state") != authorizeResp.FlowID {
		t.Errorf("authorization url state = %q, want the flow id %q", q.Get("state"), authorizeResp.FlowID)
	}

	// Stand in for the provider redirecting the browser back to the gateway.
	callback := testServer.Get(t,
		"/mcp-oauth/callback?state="+authorizeResp.FlowID+"&code=auth-code-1", "")
	defer callback.Body.Close()
	if callback.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("callback: expected 307, got %d (body: %s)",
			callback.StatusCode, testutil.ReadBody(t, callback))
	}
	if loc := callback.Header.Get("Location"); !strings.Contains(loc, "mcp_oauth=success") {
		t.Fatalf("callback did not report success: %s", loc)
	}

	redeem := testServer.Get(t, "/mcp-oauth/token/"+authorizeResp.FlowID, token)
	defer redeem.Body.Close()
	testutil.RequireStatus(t, redeem, http.StatusOK)
	var tokenResp openapi.MCPOAuthTokenResponse
	testutil.DecodeJSON(t, redeem, &tokenResp)
	if tokenResp.AuthorizationHeader == "" {
		t.Fatalf("token endpoint returned no authorization header: %+v", tokenResp)
	}
	return authorizeResp.FlowID, tokenResp.AuthorizationHeader
}

// TestMCPOAuthGrantLifecycle covers the whole MCP Gateway credential path:
// login, adoption into a durable grant when the connection is saved, and
// renewal from the refresh token once the access token has expired.
//
// This is the behavior the frozen-header design could not provide: before
// grants, the connection kept the first access token forever and started
// failing the moment the provider expired it.
func TestMCPOAuthGrantLifecycle(t *testing.T) {
	token := adminToken(t)
	agentID := createAgentReturningID(t, token, "mcp-grant-agent")
	defer deleteAgent(t, token, "mcp-grant-agent")

	// The login mints a token already inside the refresh margin, so the first
	// session must renew it. What the refresh mints is long-lived, so the
	// session after that must reuse it rather than burning another rotation.
	fake := newFakeMCPServer(t, 30*time.Second, time.Hour)
	flowID, authHeader := completeMCPLogin(t, token, fake)

	if !strings.HasPrefix(authHeader, "Bearer access-1") {
		t.Fatalf("login produced header %q, want the first minted token", authHeader)
	}
	fake.mu.Lock()
	redirects := append([]string(nil), fake.registeredRedirects...)
	fake.mu.Unlock()
	if len(redirects) != 1 || !strings.HasSuffix(redirects[0], "/api/mcp-oauth/callback") {
		t.Errorf("dynamic registration got redirect URIs %v, want the gateway callback", redirects)
	}

	// Redeeming twice is a double submit, not a second login.
	replay := testServer.Get(t, "/mcp-oauth/token/"+flowID, token)
	defer replay.Body.Close()
	testutil.RequireStatus(t, replay, http.StatusConflict)

	const connName = "smoke-mcp-grant"
	created := testServer.Post(t, "/connections", token, openapi.Connection{
		Name:               connName,
		Type:               "application",
		SubType:            services.MCPOAuthGrantSubType,
		AgentId:            agentID,
		AccessModeRunbooks: "enabled",
		AccessModeExec:     "enabled",
		AccessModeConnect:  "enabled",
		AccessSchema:       "disabled",
		Secrets: map[string]any{
			"envvar:MCP_TRANSPORT":        b64("streamable-http"),
			"envvar:MCP_AUTH":             b64("static"),
			"envvar:REMOTE_URL":           b64(fake.url("/mcp")),
			"envvar:HEADER_AUTHORIZATION": b64(authHeader),
		},
		MCPOAuthFlowID: flowID,
	})
	defer created.Body.Close()
	testutil.RequireStatus(t, created, http.StatusCreated)
	var conn openapi.Connection
	testutil.DecodeJSON(t, created, &conn)
	defer func() {
		del := testServer.Delete(t, "/connections/"+connName, token)
		del.Body.Close()
	}()

	// Adoption moved the credential out of the transient flow and into a
	// grant owned by the connection.
	grant, err := models.GetMCPOAuthGrant(models.DB, testGateway.OrgID, conn.ID, "")
	if err != nil {
		t.Fatalf("grant was not adopted for connection %s: %v", conn.ID, err)
	}
	if len(grant.RefreshTokenEncrypted) == 0 {
		t.Fatal("adopted grant carries no refresh token, so it can never be renewed")
	}
	if grant.TokenEndpoint != fake.url("/token") {
		t.Errorf("grant token endpoint = %q, want %q", grant.TokenEndpoint, fake.url("/token"))
	}
	if _, err := models.GetMCPOAuthFlow(models.DB, flowID); err == nil {
		t.Error("the flow row survived adoption; its credential is now stored twice")
	}

	issuedBefore, _ := fake.counters()

	// Session open with a token inside the refresh margin renews it.
	header, err := services.ResolveMCPOAuthHeader(t.Context(), testGateway.OrgID, conn.ID)
	if err != nil {
		t.Fatalf("resolving the grant failed: %v", err)
	}
	if header == "Bearer access-1" {
		t.Error("resolve served the expiring token instead of renewing it")
	}
	if !strings.HasPrefix(header, "Bearer access-") {
		t.Fatalf("resolve produced %q, want a bearer token", header)
	}
	if issued, _ := fake.counters(); issued != issuedBefore+1 {
		t.Errorf("expected exactly one refresh, tokens issued went %d -> %d", issuedBefore, issued)
	}

	// The renewed token is persisted, so a second resolve inside the margin
	// serves it without touching the provider again.
	issuedAfterRefresh, _ := fake.counters()
	second, err := services.ResolveMCPOAuthHeader(t.Context(), testGateway.OrgID, conn.ID)
	if err != nil {
		t.Fatalf("second resolve failed: %v", err)
	}
	if second != header {
		t.Errorf("second resolve returned %q, want the cached %q", second, header)
	}
	if issued, _ := fake.counters(); issued != issuedAfterRefresh {
		t.Errorf("second resolve refreshed again (%d -> %d); the renewal was not persisted",
			issuedAfterRefresh, issued)
	}
}

// TestMCPOAuthGrantConcurrentRefresh is the reason the grant row is read FOR
// UPDATE.
//
// The fake provider rotates its refresh token and rejects the previous one, so
// two unsynchronized refreshes leave one caller holding a dead credential and
// the grant broken for good. Every caller must come away with a working token.
func TestMCPOAuthGrantConcurrentRefresh(t *testing.T) {
	token := adminToken(t)
	agentID := createAgentReturningID(t, token, "mcp-grant-race-agent")
	defer deleteAgent(t, token, "mcp-grant-race-agent")

	// Every caller races on a token inside the margin; the winner's renewal
	// is long-lived, so exactly one rotation should satisfy all of them.
	fake := newFakeMCPServer(t, 30*time.Second, time.Hour)
	flowID, authHeader := completeMCPLogin(t, token, fake)

	const connName = "smoke-mcp-grant-race"
	created := testServer.Post(t, "/connections", token, openapi.Connection{
		Name:               connName,
		Type:               "application",
		SubType:            services.MCPOAuthGrantSubType,
		AgentId:            agentID,
		AccessModeRunbooks: "enabled",
		AccessModeExec:     "enabled",
		AccessModeConnect:  "enabled",
		AccessSchema:       "disabled",
		Secrets: map[string]any{
			"envvar:MCP_TRANSPORT":        b64("streamable-http"),
			"envvar:MCP_AUTH":             b64("static"),
			"envvar:REMOTE_URL":           b64(fake.url("/mcp")),
			"envvar:HEADER_AUTHORIZATION": b64(authHeader),
		},
		MCPOAuthFlowID: flowID,
	})
	defer created.Body.Close()
	testutil.RequireStatus(t, created, http.StatusCreated)
	var conn openapi.Connection
	testutil.DecodeJSON(t, created, &conn)
	defer func() {
		del := testServer.Delete(t, "/connections/"+connName, token)
		del.Body.Close()
	}()

	const callers = 8
	results := make([]string, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i], errs[i] = services.ResolveMCPOAuthHeader(t.Context(), testGateway.OrgID, conn.ID)
		}()
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d failed to resolve the grant: %v", i, err)
		}
		if !strings.HasPrefix(results[i], "Bearer access-") {
			t.Fatalf("caller %d got %q, want a bearer token", i, results[i])
		}
	}

	// The grant must still be usable afterwards: a lost rotation shows up
	// here as invalid_grant.
	if _, err := services.ResolveMCPOAuthHeader(t.Context(), testGateway.OrgID, conn.ID); err != nil {
		t.Fatalf("the grant was broken by concurrent refreshes: %v", err)
	}
	// Exactly one, not "at most callers": the row lock is what collapses the
	// burst, and the first refresh persists a token good for an hour, so
	// every caller that blocks on the lock must find it and skip the
	// provider entirely. Two refreshes means the lock did not serialize.
	if _, refreshes := fake.counters(); refreshes != 1 {
		t.Errorf("provider saw %d refresh grants for %d concurrent callers, want exactly 1", refreshes, callers)
	}
}

// A provider that does not rotate refresh tokens omits refresh_token from the
// refresh response, meaning "keep using the one you have". Persisting that
// omission as an empty value erases the only credential that can renew the
// grant: the first refresh appears to work, and every session after it is
// stranded on an expiring access token with no way back.
//
// Both token-endpoint auth methods are covered because they take different
// code paths to the same persist call: "none" delegates to the oauth library,
// while client_secret_post uses Hoop's own request builder. Only the second
// reaches persistRefreshedGrant with an empty RefreshToken, so a test that
// exercised only the first would pass with the erase fully restored.
//
// Each grant must survive two renewals across three session opens.
func TestMCPOAuthGrantSurvivesNonRotatingProvider(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		suffix                 string
		clientID, clientSecret string
	}{
		{name: "public client (auth method none)", suffix: "pub"},
		{name: "confidential client (client_secret_post)", suffix: "conf",
			clientID: "client-conf", clientSecret: "s3cret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token := adminToken(t)
			agentName := "mcp-grant-norotate-agent-" + tc.suffix
			agentID := createAgentReturningID(t, token, agentName)
			defer deleteAgent(t, token, agentName)

			// Every token lands inside the refresh margin, so each session
			// open renews and the stored refresh token is used again.
			fake := newFakeMCPServer(t, 30*time.Second, 30*time.Second)
			fake.keepRefreshToken = true
			flowID, authHeader := completeMCPLoginAs(t, token, fake, tc.clientID, tc.clientSecret)

			connName := "smoke-mcp-grant-norotate-" + tc.suffix
			created := testServer.Post(t, "/connections", token, openapi.Connection{
				Name:               connName,
				Type:               "application",
				SubType:            services.MCPOAuthGrantSubType,
				AgentId:            agentID,
				AccessModeRunbooks: "enabled",
				AccessModeExec:     "enabled",
				AccessModeConnect:  "enabled",
				AccessSchema:       "disabled",
				Secrets: map[string]any{
					"envvar:MCP_TRANSPORT":        b64("streamable-http"),
					"envvar:MCP_AUTH":             b64("static"),
					"envvar:REMOTE_URL":           b64(fake.url("/mcp")),
					"envvar:HEADER_AUTHORIZATION": b64(authHeader),
				},
				MCPOAuthFlowID: flowID,
			})
			defer created.Body.Close()
			testutil.RequireStatus(t, created, http.StatusCreated)
			var conn openapi.Connection
			testutil.DecodeJSON(t, created, &conn)
			defer func() {
				del := testServer.Delete(t, "/connections/"+connName, token)
				del.Body.Close()
			}()

			original, err := models.GetMCPOAuthGrant(models.DB, testGateway.OrgID, conn.ID, "")
			if err != nil {
				t.Fatalf("grant was not adopted: %v", err)
			}
			wantMethod := "none"
			if tc.clientSecret != "" {
				wantMethod = "client_secret_post"
			}
			if original.TokenAuthMethod != wantMethod {
				t.Fatalf("grant token auth method = %q, want %q; this case is not exercising the intended refresh path",
					original.TokenAuthMethod, wantMethod)
			}
			originalRefresh := decryptGrantRefreshToken(t, original)

			// Three session opens, each one refreshing. The second is what
			// fails when the omission is persisted: the grant no longer has a
			// refresh token to renew with.
			for i := range 3 {
				header, err := services.ResolveMCPOAuthHeader(t.Context(), testGateway.OrgID, conn.ID)
				if err != nil {
					t.Fatalf("session open %d could not resolve the grant: %v", i+1, err)
				}
				if !strings.HasPrefix(header, "Bearer access-") {
					t.Fatalf("session open %d produced %q, want a bearer token", i+1, header)
				}
				stored, err := models.GetMCPOAuthGrant(models.DB, testGateway.OrgID, conn.ID, "")
				if err != nil {
					t.Fatalf("grant disappeared after session open %d: %v", i+1, err)
				}
				if got := decryptGrantRefreshToken(t, stored); got != originalRefresh {
					t.Fatalf("session open %d left refresh token %q, want the original %q preserved",
						i+1, got, originalRefresh)
				}
			}

			if _, refreshes := fake.counters(); refreshes != 3 {
				t.Errorf("provider saw %d refresh grants, want 3 (one per session open)", refreshes)
			}
		})
	}
}

// decryptGrantRefreshToken returns the grant's stored refresh token in the
// clear, failing the test when the grant has none.
func decryptGrantRefreshToken(t *testing.T, grant *models.MCPOAuthGrant) string {
	t.Helper()
	if len(grant.RefreshTokenEncrypted) == 0 {
		t.Fatal("grant carries no refresh token; it can never be renewed again")
	}
	plain, err := models.DecryptCredentialSecretKey(grant.RefreshTokenEncrypted)
	if err != nil {
		t.Fatalf("failed decrypting the stored refresh token: %v", err)
	}
	return plain
}

// TestMCPOAuthGrantSkippedForLegacySubtype pins the blast radius: only the
// mcpproxy subtype gets a grant. The legacy mcp subtype keeps the frozen
// header it has always had.
func TestMCPOAuthGrantSkippedForLegacySubtype(t *testing.T) {
	token := adminToken(t)
	agentID := createAgentReturningID(t, token, "mcp-legacy-agent")
	defer deleteAgent(t, token, "mcp-legacy-agent")

	fake := newFakeMCPServer(t, time.Hour, time.Hour)
	flowID, authHeader := completeMCPLogin(t, token, fake)

	const connName = "smoke-mcp-legacy"
	created := testServer.Post(t, "/connections", token, openapi.Connection{
		Name:               connName,
		Type:               "application",
		SubType:            "mcp",
		AgentId:            agentID,
		AccessModeRunbooks: "enabled",
		AccessModeExec:     "enabled",
		AccessModeConnect:  "enabled",
		AccessSchema:       "disabled",
		Secrets: map[string]any{
			"envvar:REMOTE_URL":           b64(fake.url("/mcp")),
			"envvar:HEADER_AUTHORIZATION": b64(authHeader),
		},
		MCPOAuthFlowID: flowID,
	})
	defer created.Body.Close()
	testutil.RequireStatus(t, created, http.StatusCreated)
	var conn openapi.Connection
	testutil.DecodeJSON(t, created, &conn)
	defer func() {
		del := testServer.Delete(t, "/connections/"+connName, token)
		del.Body.Close()
	}()

	if _, err := models.GetMCPOAuthGrant(models.DB, testGateway.OrgID, conn.ID, ""); err == nil {
		t.Error("the legacy mcp subtype adopted a grant; only mcpproxy should")
	}
	// Resolving is a no-op for a connection with no grant, and must not
	// disturb the session.
	header, err := services.ResolveMCPOAuthHeader(t.Context(), testGateway.OrgID, conn.ID)
	if err != nil {
		t.Fatalf("resolving a grant-less connection errored: %v", err)
	}
	if header != "" {
		t.Errorf("resolving a grant-less connection produced %q, want no header", header)
	}
}

// createMCPProxyConnection saves an mcpproxy connection pointing at
// remoteURL and adopting flowID, and returns the decoded response. The caller
// gets the response body rather than just the connection because the adoption
// warning rides on it.
func createMCPProxyConnection(t *testing.T, token, name, agentID, remoteURL, authHeader, flowID string) openapi.Connection {
	t.Helper()
	created := testServer.Post(t, "/connections", token, openapi.Connection{
		Name:               name,
		Type:               "application",
		SubType:            services.MCPOAuthGrantSubType,
		AgentId:            agentID,
		AccessModeRunbooks: "enabled",
		AccessModeExec:     "enabled",
		AccessModeConnect:  "enabled",
		AccessSchema:       "disabled",
		Secrets: map[string]any{
			"envvar:MCP_TRANSPORT":        b64("streamable-http"),
			"envvar:MCP_AUTH":             b64("static"),
			"envvar:REMOTE_URL":           b64(remoteURL),
			"envvar:HEADER_AUTHORIZATION": b64(authHeader),
		},
		MCPOAuthFlowID: flowID,
	})
	defer created.Body.Close()
	testutil.RequireStatus(t, created, http.StatusCreated)
	var conn openapi.Connection
	testutil.DecodeJSON(t, created, &conn)
	t.Cleanup(func() {
		del := testServer.Delete(t, "/connections/"+name, token)
		del.Body.Close()
	})
	return conn
}

// A provider that answers invalid_grant has revoked the credential: nothing
// the gateway holds can renew it, and the grant row is dead.
//
// The row must actually be gone afterwards. It used to be deleted inside the
// transaction that then returned the error reporting the rejection, so GORM
// rolled the DELETE back and the dead grant survived — every subsequent
// session open replayed a refresh token the provider had already refused,
// silently and forever. The second resolve below is the assertion that
// matters: it must report a connection with no grant, not another rejection.
func TestMCPOAuthGrantDeletedWhenProviderRejectsIt(t *testing.T) {
	token := adminToken(t)
	agentID := createAgentReturningID(t, token, "mcp-grant-revoked-agent")
	defer deleteAgent(t, token, "mcp-grant-revoked-agent")

	// The login mints a token already inside the refresh margin, so the first
	// session open must go to the provider — which by then has revoked it.
	fake := newFakeMCPServer(t, 30*time.Second, time.Hour)
	flowID, authHeader := completeMCPLogin(t, token, fake)
	conn := createMCPProxyConnection(t, token, "smoke-mcp-grant-revoked", agentID, fake.url("/mcp"), authHeader, flowID)

	if _, err := models.GetMCPOAuthGrant(models.DB, testGateway.OrgID, conn.ID, ""); err != nil {
		t.Fatalf("grant was not adopted: %v", err)
	}

	fake.mu.Lock()
	fake.revokeRefresh = true
	fake.mu.Unlock()

	if _, err := services.ResolveMCPOAuthHeader(t.Context(), testGateway.OrgID, conn.ID); err == nil {
		t.Fatal("resolve succeeded against a provider that rejected the credential")
	}
	if _, err := models.GetMCPOAuthGrant(models.DB, testGateway.OrgID, conn.ID, ""); err == nil {
		t.Fatal("the rejected grant survived; every session open will replay the dead refresh token")
	}

	_, refreshesAfterRejection := fake.counters()

	// With the grant gone this is a connection with no grant, which resolves
	// to the empty header and no error — the clean "not authorized" the
	// deletion exists to produce.
	header, err := services.ResolveMCPOAuthHeader(t.Context(), testGateway.OrgID, conn.ID)
	if err != nil {
		t.Fatalf("the session after a rejection still reports an error instead of no grant: %v", err)
	}
	if header != "" {
		t.Errorf("resolve produced %q after the grant was dropped, want no header", header)
	}
	if _, refreshes := fake.counters(); refreshes != refreshesAfterRejection {
		t.Errorf("the provider saw another refresh (%d -> %d); the dead credential is still being replayed",
			refreshesAfterRejection, refreshes)
	}
}

// An admin can authorize against one MCP server and then edit the URL field
// before saving. The flow id in the payload still points at the server that
// was authorized, so adopting it would durably attach that server's refresh
// token to a connection that talks to a different one and auto-renew it
// forever — credential exfiltration performed by editing a form field.
//
// The save still succeeds (the connection row is written and its frozen
// header works), but no grant may exist and the response must say so.
func TestMCPOAuthGrantRefusesCrossServerAdoption(t *testing.T) {
	token := adminToken(t)
	agentID := createAgentReturningID(t, token, "mcp-grant-crossserver-agent")
	defer deleteAgent(t, token, "mcp-grant-crossserver-agent")

	authorized := newFakeMCPServer(t, time.Hour, time.Hour)
	other := newFakeMCPServer(t, time.Hour, time.Hour)
	flowID, authHeader := completeMCPLogin(t, token, authorized)

	conn := createMCPProxyConnection(t, token, "smoke-mcp-grant-crossserver", agentID,
		other.url("/mcp"), authHeader, flowID)

	if _, err := models.GetMCPOAuthGrant(models.DB, testGateway.OrgID, conn.ID, ""); err == nil {
		t.Fatal("the connection adopted a grant for a server it does not talk to")
	}
	if conn.MCPOAuthWarning == "" {
		t.Fatal("the create response reported no warning; the admin sees a 201 for a connection that will degrade")
	}
}

// The matching adoption must still work, or the refusal above would be
// indistinguishable from adoption being broken.
func TestMCPOAuthGrantAdoptsMatchingEndpoint(t *testing.T) {
	token := adminToken(t)
	agentID := createAgentReturningID(t, token, "mcp-grant-match-agent")
	defer deleteAgent(t, token, "mcp-grant-match-agent")

	fake := newFakeMCPServer(t, time.Hour, time.Hour)
	flowID, authHeader := completeMCPLogin(t, token, fake)

	// A trailing slash the admin's browser added must not read as a
	// different server.
	conn := createMCPProxyConnection(t, token, "smoke-mcp-grant-match", agentID,
		fake.url("/mcp")+"/", authHeader, flowID)

	if _, err := models.GetMCPOAuthGrant(models.DB, testGateway.OrgID, conn.ID, ""); err != nil {
		t.Fatalf("adoption was refused for the connection's own endpoint: %v", err)
	}
	if conn.MCPOAuthWarning != "" {
		t.Errorf("a successful adoption reported a warning: %s", conn.MCPOAuthWarning)
	}
}

// The callback is unauthenticated by design — the provider redirects the
// browser there — so anyone holding the URL can replay it, and a back button
// is enough. A replay used to run the failure path (the authorization code is
// spent, so the exchange fails), flipping a completed flow to error and
// destroying the credential the connection was about to adopt: an
// unauthenticated request silently breaking a connection an admin just saved.
func TestMCPOAuthCallbackReplayDoesNotDowngradeAFlow(t *testing.T) {
	token := adminToken(t)
	agentID := createAgentReturningID(t, token, "mcp-callback-replay-agent")
	defer deleteAgent(t, token, "mcp-callback-replay-agent")

	fake := newFakeMCPServer(t, time.Hour, time.Hour)
	// completeMCPLogin drives the callback once and then redeems the token,
	// leaving the flow consumed and still adoptable.
	flowID, authHeader := completeMCPLogin(t, token, fake)

	replay := testServer.Get(t, "/mcp-oauth/callback?state="+flowID+"&code=auth-code-1", "")
	defer replay.Body.Close()
	if replay.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("callback replay: expected 307, got %d (body: %s)", replay.StatusCode, testutil.ReadBody(t, replay))
	}
	if loc := replay.Header.Get("Location"); strings.Contains(loc, "mcp_oauth=error") {
		t.Fatalf("the replay downgraded a good flow to error: %s", loc)
	}

	flow, err := models.GetMCPOAuthFlow(models.DB, flowID)
	if err != nil {
		t.Fatalf("the replay destroyed the flow row: %v", err)
	}
	if flow.Status != models.MCPOAuthFlowStatusConsumed {
		t.Fatalf("flow status = %q after a replay, want it left %q", flow.Status, models.MCPOAuthFlowStatusConsumed)
	}

	// The point of not downgrading: the connection saved afterwards still
	// gets its grant.
	conn := createMCPProxyConnection(t, token, "smoke-mcp-callback-replay", agentID,
		fake.url("/mcp"), authHeader, flowID)
	if _, err := models.GetMCPOAuthGrant(models.DB, testGateway.OrgID, conn.ID, ""); err != nil {
		t.Fatalf("the callback replay killed grant adoption for a connection saved after it: %v", err)
	}
}

// The resource wizard creates a resource and its roles in one POST
// /resources, and it runs the same OAuth login the standalone connection form
// runs. Until the flow id was plumbed through ResourceRoleRequest the field
// was simply dropped by the JSON binding: no adoption was attempted, no
// warning could exist, and every mcpproxy role created through the wizard
// kept a token nothing would ever renew.
func TestMCPOAuthGrantAdoptedForAWizardCreatedRole(t *testing.T) {
	token := adminToken(t)
	agentID := createAgentReturningID(t, token, "mcp-grant-wizard-agent")
	defer deleteAgent(t, token, "mcp-grant-wizard-agent")

	fake := newFakeMCPServer(t, time.Hour, time.Hour)
	flowID, authHeader := completeMCPLogin(t, token, fake)

	resource := createMCPProxyResource(t, token, "smoke-mcp-wizard", "smoke-mcp-wizard-role",
		agentID, fake.url("/mcp"), authHeader, flowID)

	conn, err := models.GetConnectionByNameOrID(
		models.NewAdminContext(testGateway.OrgID), "smoke-mcp-wizard-role")
	if err != nil || conn == nil {
		t.Fatalf("the wizard's role was not created: %v", err)
	}
	if _, err := models.GetMCPOAuthGrant(models.DB, testGateway.OrgID, conn.ID, ""); err != nil {
		t.Fatalf("the wizard's role got no grant, so its credential dies at the provider's TTL: %v", err)
	}
	if len(resource.MCPOAuthWarnings) != 0 {
		t.Errorf("a successful adoption reported warnings: %v", resource.MCPOAuthWarnings)
	}
}

// The wizard's counterpart to TestMCPOAuthGrantRefusesCrossServerAdoption: the
// same refusal must apply, and — because the wizard creates several roles at
// once — must name the role it applies to. A bare "something went wrong" would
// leave the admin guessing which of the connections they just created is the
// degraded one.
func TestMCPOAuthGrantRefusalNamesTheWizardRole(t *testing.T) {
	token := adminToken(t)
	agentID := createAgentReturningID(t, token, "mcp-grant-wizard-x-agent")
	defer deleteAgent(t, token, "mcp-grant-wizard-x-agent")

	authorized := newFakeMCPServer(t, time.Hour, time.Hour)
	other := newFakeMCPServer(t, time.Hour, time.Hour)
	flowID, authHeader := completeMCPLogin(t, token, authorized)

	const roleName = "smoke-mcp-wizard-x-role"
	resource := createMCPProxyResource(t, token, "smoke-mcp-wizard-x", roleName,
		agentID, other.url("/mcp"), authHeader, flowID)

	conn, err := models.GetConnectionByNameOrID(
		models.NewAdminContext(testGateway.OrgID), roleName)
	if err != nil || conn == nil {
		t.Fatalf("the wizard's role was not created: %v", err)
	}
	if _, err := models.GetMCPOAuthGrant(models.DB, testGateway.OrgID, conn.ID, ""); err == nil {
		t.Fatal("the wizard's role adopted a grant for a server it does not talk to")
	}
	if len(resource.MCPOAuthWarnings) != 1 {
		t.Fatalf("warnings = %v, want exactly one; the admin sees a 201 for a role that will degrade",
			resource.MCPOAuthWarnings)
	}
	if got := resource.MCPOAuthWarnings[0].Name; got != roleName {
		t.Errorf("warning names role %q, want %q", got, roleName)
	}
	if resource.MCPOAuthWarnings[0].Warning == "" {
		t.Error("the warning carries no reason, so the admin cannot tell a mismatch from an expired flow")
	}
}

// createMCPProxyResource drives the wizard's request: one resource carrying
// one mcpproxy role that adopts flowID. Returns the decoded response, which is
// where the per-role adoption warnings ride.
func createMCPProxyResource(t *testing.T, token, resourceName, roleName, agentID, remoteURL, authHeader, flowID string) openapi.ResourceResponse {
	t.Helper()
	created := testServer.Post(t, "/resources", token, openapi.ResourceRequest{
		Name:    resourceName,
		Type:    "application",
		SubType: services.MCPOAuthGrantSubType,
		AgentID: agentID,
		EnvVars: map[string]string{},
		Roles: []openapi.ResourceRoleRequest{{
			Name:    roleName,
			Type:    "application",
			SubType: services.MCPOAuthGrantSubType,
			Secrets: map[string]any{
				"envvar:MCP_TRANSPORT":        b64("streamable-http"),
				"envvar:MCP_AUTH":             b64("static"),
				"envvar:REMOTE_URL":           b64(remoteURL),
				"envvar:HEADER_AUTHORIZATION": b64(authHeader),
			},
			MCPOAuthFlowID: flowID,
		}},
	})
	defer created.Body.Close()
	testutil.RequireStatus(t, created, http.StatusCreated)
	var resource openapi.ResourceResponse
	testutil.DecodeJSON(t, created, &resource)
	t.Cleanup(func() {
		// Connections must go before the resource.
		del := testServer.Delete(t, "/connections/"+roleName, token)
		del.Body.Close()
		del = testServer.Delete(t, "/resources/"+resourceName, token)
		del.Body.Close()
	})
	return resource
}

// b64 encodes a connection env var value the way the webapp does: the wire
// contract is "envvar:NAME" -> base64 plaintext.
func b64(v string) string { return base64.StdEncoding.EncodeToString([]byte(v)) }
