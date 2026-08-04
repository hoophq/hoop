package services

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hoophq/mcpproxy/auth/outbound/oauth"
)

// fakeResourceServer publishes RFC 9728 protected-resource metadata and RFC
// 8414 authorization-server metadata over loopback, which discovery accepts
// as plaintext. Both documents are supplied by the caller so a test can
// publish exactly the hostile shape it wants to see refused.
func fakeResourceServer(t *testing.T, prm, asMeta func(base string) map[string]any) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	writeJSON := func(w http.ResponseWriter, body map[string]any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}
	prmHandler := func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, prm(srv.URL)) }
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", prmHandler)
	mux.HandleFunc("/.well-known/oauth-protected-resource", prmHandler)
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, asMeta(srv.URL))
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func defaultASMetadata(base string) map[string]any {
	return map[string]any{
		"issuer":                           base,
		"authorization_endpoint":           base + "/authorize",
		"token_endpoint":                   base + "/token",
		"registration_endpoint":            base + "/register",
		"code_challenge_methods_supported": []string{"S256"},
	}
}

// RFC 9728 §3.3: a protected resource's metadata must name that resource and
// no other. A server that advertises someone else's identifier turns Hoop into
// a confused deputy — the RFC 8707 resource indicator Hoop sends is taken from
// this field, so the authorization server would mint a token audienced for a
// resource the admin never chose and hand it to the server that asked for it.
func TestDiscoverRejectsForeignResourceIdentifier(t *testing.T) {
	srv := fakeResourceServer(t,
		func(base string) map[string]any {
			return map[string]any{
				"resource":              "https://victim.example.com/mcp",
				"authorization_servers": []string{base},
			}
		},
		defaultASMetadata)

	_, err := DiscoverMCPAuthServer(t.Context(), srv.URL+"/mcp")
	if err == nil {
		t.Fatal("discovery accepted metadata advertising another resource's identifier")
	}
	if !strings.Contains(err.Error(), "victim.example.com") {
		t.Fatalf("error does not name the offending identifier: %v", err)
	}
}

// The resource may describe itself under either identifier discovery could
// have used: the full MCP endpoint or its origin.
func TestDiscoverAcceptsOwnResourceIdentifier(t *testing.T) {
	for _, tc := range []struct {
		name     string
		resource func(base string) string
	}{
		{"full endpoint", func(base string) string { return base + "/mcp" }},
		{"origin", func(base string) string { return base }},
		{"omitted", func(string) string { return "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := fakeResourceServer(t,
				func(base string) map[string]any {
					return map[string]any{
						"resource":              tc.resource(base),
						"authorization_servers": []string{base},
					}
				},
				defaultASMetadata)

			if _, err := DiscoverMCPAuthServer(t.Context(), srv.URL+"/mcp"); err != nil {
				t.Fatalf("discovery refused a resource describing itself: %v", err)
			}
		})
	}
}

// A discovered document is attacker-influenced input. An endpoint served over
// plain http on a routable host would put the client secret, the authorization
// code and the refresh token on the wire in cleartext, so discovery refuses it
// even though the MCP server itself answered over an accepted scheme.
func TestDiscoverRejectsPlaintextEndpoints(t *testing.T) {
	for _, endpoint := range []string{"authorization_endpoint", "token_endpoint", "registration_endpoint"} {
		t.Run(endpoint, func(t *testing.T) {
			srv := fakeResourceServer(t,
				func(base string) map[string]any {
					return map[string]any{"resource": base + "/mcp", "authorization_servers": []string{base}}
				},
				func(base string) map[string]any {
					md := defaultASMetadata(base)
					md[endpoint] = "http://evil.example.com/" + endpoint
					return md
				})

			_, err := DiscoverMCPAuthServer(t.Context(), srv.URL+"/mcp")
			if err == nil {
				t.Fatalf("discovery accepted a plaintext %s", endpoint)
			}
			if !strings.Contains(err.Error(), endpoint) || !strings.Contains(err.Error(), "https") {
				t.Fatalf("error does not identify the insecure %s: %v", endpoint, err)
			}
		})
	}
}

// Loopback stays plaintext-friendly: running an MCP server on a laptop is the
// case http:// exists for, and that traffic never leaves the machine.
func TestDiscoverAllowsLoopbackPlaintextEndpoints(t *testing.T) {
	srv := fakeResourceServer(t,
		func(base string) map[string]any {
			return map[string]any{"resource": base + "/mcp", "authorization_servers": []string{base}}
		},
		func(base string) map[string]any {
			md := defaultASMetadata(base)
			md["token_endpoint"] = "http://localhost:9999/token"
			return md
		})

	if _, err := DiscoverMCPAuthServer(t.Context(), srv.URL+"/mcp"); err != nil {
		t.Fatalf("discovery refused a loopback endpoint: %v", err)
	}
}

// The token endpoint is reached with the client secret and the refresh token
// in hand. Following a redirect out of that POST re-sends both to a host the
// authorization server chose, over a scheme it chose — so the client refuses
// instead, and the refusal surfaces as an error rather than a silent hop.
func TestTokenEndpointRefusesToFollowRedirects(t *testing.T) {
	var landed bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		landed = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"stolen","token_type":"Bearer"}`))
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/token", http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()

	cfg := oauth.ClientConfig{
		ClientID:      "client-1",
		ClientSecret:  "s3cret",
		TokenEndpoint: redirector.URL + "/token",
		HTTPClient:    mcpOAuthHTTPClient,
	}
	for _, method := range []string{tokenAuthMethodNone, tokenAuthMethodClientSecretPost} {
		t.Run(method, func(t *testing.T) {
			landed = false
			if _, err := postRefreshGrant(t.Context(), cfg, method, "refresh-1"); err == nil {
				t.Fatal("the refresh grant followed a redirect out of a credentialed POST")
			}
			if landed {
				t.Fatal("the client secret and refresh token were re-sent to the redirect target")
			}
		})
	}
}

// A grant row written before transport security was enforced would otherwise
// keep shipping its refresh token in cleartext at every session open, so the
// endpoint is re-checked on the refresh path rather than trusted from
// discovery.
//
// The assertion is on the refusal, not on "an error": without the check this
// call still fails, but it fails after trying to reach the host — which is
// the failure mode being prevented, not evidence of prevention.
func TestRefreshRejectsPlaintextTokenEndpoint(t *testing.T) {
	cfg := oauth.ClientConfig{
		ClientID:      "client-1",
		TokenEndpoint: "http://legacy.example.com/token",
		HTTPClient:    mcpOAuthHTTPClient,
	}
	_, err := postRefreshGrant(t.Context(), cfg, tokenAuthMethodNone, "refresh-1")
	if err == nil {
		t.Fatal("the refresh grant posted a refresh token to a plaintext endpoint")
	}
	if !strings.Contains(err.Error(), "does not use https") {
		t.Fatalf("the refresh grant reached the network before refusing: %v", err)
	}
}
