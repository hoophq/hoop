// MCP OAuth client engine.
//
// This file implements the OAuth 2.1 *client* logic Hoop performs on an
// admin's behalf when creating an "mcp" httpproxy connection against a remote
// MCP server that protects its endpoint with OAuth (e.g. https://mcp.figma.com/mcp).
//
// It follows the Model Context Protocol authorization profile:
//   - RFC 9728 Protected Resource Metadata discovery (find the auth server)
//   - RFC 8414 / OpenID Connect Discovery (find authorize/token endpoints)
//   - RFC 7591 Dynamic Client Registration (when no client_id is supplied)
//   - Authorization Code + PKCE (RFC 7636) with RFC 8707 resource binding
//
// The HTTP handlers that drive these helpers across the authorize/callback/token
// hops live in connection_mcp_oauth.go.
package apiconnections

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// mcpDiscoveryTimeout bounds each outbound discovery / registration / token
// request to the upstream MCP authorization server.
const mcpDiscoveryTimeout = 15 * time.Second

// mcpOAuthHTTPClient is the bounded client used for all upstream OAuth calls.
var mcpOAuthHTTPClient = &http.Client{Timeout: mcpDiscoveryTimeout}

// mcpProtectedResourceMetadata is the RFC 9728 document published by the MCP
// server. Only the fields Hoop consumes are modeled.
type mcpProtectedResourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
	ScopesSupported      []string `json:"scopes_supported"`
}

// mcpAuthServerMetadata is the RFC 8414 / OIDC discovery document published by
// the upstream authorization server. Only the fields Hoop consumes are modeled.
type mcpAuthServerMetadata struct {
	Issuer                        string   `json:"issuer"`
	AuthorizationEndpoint         string   `json:"authorization_endpoint"`
	TokenEndpoint                 string   `json:"token_endpoint"`
	RegistrationEndpoint          string   `json:"registration_endpoint"`
	ScopesSupported               []string `json:"scopes_supported"`
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported"`
	TokenEndpointAuthMethods      []string `json:"token_endpoint_auth_methods_supported"`
}

// mcpDiscovery is the resolved set of endpoints + metadata Hoop needs to drive
// the authorization code flow for a given MCP server URL.
type mcpDiscovery struct {
	Resource              string
	Issuer                string
	AuthorizationEndpoint string
	TokenEndpoint         string
	RegistrationEndpoint  string
	ScopesSupported       []string
}

// mcpTokenResponse is the RFC 6749 token endpoint response.
type mcpTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

// discoverMCPAuthServer resolves the upstream authorization server endpoints
// for an MCP server URL.
//
// Per the MCP spec, the MCP endpoint (the "resource") advertises its
// authorization server(s) through RFC 9728 protected-resource metadata. When
// that document is unavailable, the server URL's origin is treated as the
// authorization server issuer and metadata is discovered directly (RFC 8414 /
// OIDC) — this covers servers that co-locate the resource and auth server.
func discoverMCPAuthServer(ctx context.Context, serverURL string) (*mcpDiscovery, error) {
	resourceURL, err := url.Parse(serverURL)
	if err != nil || !resourceURL.IsAbs() || resourceURL.Host == "" {
		return nil, fmt.Errorf("server_url must be an absolute URL")
	}
	if resourceURL.Scheme != "https" && resourceURL.Hostname() != "localhost" && resourceURL.Hostname() != "127.0.0.1" {
		return nil, fmt.Errorf("server_url must use https")
	}

	resource := strings.TrimSuffix(serverURL, "/")
	issuer := ""
	var scopes []string

	if prm, err := fetchProtectedResourceMetadata(ctx, resourceURL); err == nil && prm != nil && len(prm.AuthorizationServers) > 0 {
		issuer = strings.TrimSpace(prm.AuthorizationServers[0])
		if prm.Resource != "" {
			resource = prm.Resource
		}
		scopes = prm.ScopesSupported
	}

	// Fallback: no usable protected-resource metadata. Treat the server origin
	// as the authorization server issuer.
	if issuer == "" {
		issuer = fmt.Sprintf("%s://%s", resourceURL.Scheme, resourceURL.Host)
	}

	asMeta, err := fetchAuthServerMetadata(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("failed discovering authorization server metadata: %w", err)
	}
	if asMeta.AuthorizationEndpoint == "" || asMeta.TokenEndpoint == "" {
		return nil, fmt.Errorf("authorization server metadata is missing authorization_endpoint or token_endpoint")
	}
	if len(scopes) == 0 {
		scopes = asMeta.ScopesSupported
	}

	return &mcpDiscovery{
		Resource:              resource,
		Issuer:                strings.TrimRight(issuer, "/"),
		AuthorizationEndpoint: asMeta.AuthorizationEndpoint,
		TokenEndpoint:         asMeta.TokenEndpoint,
		RegistrationEndpoint:  asMeta.RegistrationEndpoint,
		ScopesSupported:       scopes,
	}, nil
}

// fetchProtectedResourceMetadata attempts RFC 9728 discovery against the
// resource URL. Per RFC 9728 §3.1 the well-known segment is inserted before
// the resource path; the root form is tried as a fallback. A miss is not an
// error (the caller falls back to direct AS discovery).
func fetchProtectedResourceMetadata(ctx context.Context, resourceURL *url.URL) (*mcpProtectedResourceMetadata, error) {
	origin := fmt.Sprintf("%s://%s", resourceURL.Scheme, resourceURL.Host)
	path := strings.TrimSuffix(resourceURL.EscapedPath(), "/")
	candidates := []string{origin + "/.well-known/oauth-protected-resource"}
	if path != "" {
		candidates = []string{
			origin + "/.well-known/oauth-protected-resource" + path,
			origin + "/.well-known/oauth-protected-resource",
		}
	}
	var lastErr error
	for _, candidate := range candidates {
		var meta mcpProtectedResourceMetadata
		if err := getJSON(ctx, candidate, &meta); err != nil {
			lastErr = err
			continue
		}
		return &meta, nil
	}
	return nil, lastErr
}

// fetchAuthServerMetadata attempts RFC 8414 and OIDC discovery against the
// issuer. Both well-known forms are tried because MCP servers vary in which
// they publish. Per RFC 8414 §3.1, when the issuer carries a path the
// well-known segment is inserted before it.
func fetchAuthServerMetadata(ctx context.Context, issuer string) (*mcpAuthServerMetadata, error) {
	issuerURL, err := url.Parse(issuer)
	if err != nil || !issuerURL.IsAbs() {
		return nil, fmt.Errorf("authorization server issuer is not a valid URL")
	}
	origin := fmt.Sprintf("%s://%s", issuerURL.Scheme, issuerURL.Host)
	path := strings.TrimSuffix(issuerURL.EscapedPath(), "/")

	var candidates []string
	if path == "" {
		candidates = []string{
			origin + "/.well-known/oauth-authorization-server",
			origin + "/.well-known/openid-configuration",
		}
	} else {
		candidates = []string{
			origin + "/.well-known/oauth-authorization-server" + path,
			origin + path + "/.well-known/openid-configuration",
			origin + "/.well-known/openid-configuration" + path,
		}
	}

	var lastErr error
	for _, candidate := range candidates {
		var meta mcpAuthServerMetadata
		if err := getJSON(ctx, candidate, &meta); err != nil {
			lastErr = err
			continue
		}
		if meta.AuthorizationEndpoint != "" && meta.TokenEndpoint != "" {
			return &meta, nil
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no authorization server metadata document found for issuer %q", issuer)
	}
	return nil, lastErr
}

// mcpClientRegistration holds the credentials produced by RFC 7591 Dynamic
// Client Registration (or supplied directly by the admin).
type mcpClientRegistration struct {
	ClientID        string
	ClientSecret    string
	TokenAuthMethod string
}

// registerMCPClient performs RFC 7591 Dynamic Client Registration against the
// authorization server, registering Hoop's callback as the redirect URI.
func registerMCPClient(ctx context.Context, registrationEndpoint, redirectURI string) (*mcpClientRegistration, error) {
	if registrationEndpoint == "" {
		return nil, fmt.Errorf("the authorization server does not support dynamic client registration; provide client_id and client_secret")
	}
	reqBody := map[string]any{
		"client_name":                "Hoop",
		"redirect_uris":              []string{redirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, registrationEndpoint, strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := mcpOAuthHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dynamic client registration request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("dynamic client registration failed: status=%d body=%s", resp.StatusCode, truncate(string(body), 300))
	}
	var reg struct {
		ClientID        string `json:"client_id"`
		ClientSecret    string `json:"client_secret"`
		TokenAuthMethod string `json:"token_endpoint_auth_method"`
	}
	if err := json.Unmarshal(body, &reg); err != nil {
		return nil, fmt.Errorf("invalid dynamic client registration response: %w", err)
	}
	if reg.ClientID == "" {
		return nil, fmt.Errorf("dynamic client registration response did not include a client_id")
	}
	method := reg.TokenAuthMethod
	if method == "" {
		if reg.ClientSecret == "" {
			method = "none"
		} else {
			method = "client_secret_post"
		}
	}
	return &mcpClientRegistration{
		ClientID:        reg.ClientID,
		ClientSecret:    reg.ClientSecret,
		TokenAuthMethod: method,
	}, nil
}

// buildMCPAuthorizationURL constructs the authorization request URL for the
// authorization code + PKCE flow, including the RFC 8707 resource indicator.
func buildMCPAuthorizationURL(d *mcpDiscovery, clientID, redirectURI, state, codeChallenge, scopes string) (string, error) {
	authURL, err := url.Parse(d.AuthorizationEndpoint)
	if err != nil {
		return "", fmt.Errorf("invalid authorization_endpoint: %w", err)
	}
	q := authURL.Query()
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("code_challenge", codeChallenge)
	q.Set("code_challenge_method", "S256")
	if d.Resource != "" {
		q.Set("resource", d.Resource)
	}
	if scopes != "" {
		q.Set("scope", scopes)
	}
	authURL.RawQuery = q.Encode()
	return authURL.String(), nil
}

// exchangeMCPCode exchanges an authorization code for tokens at the token
// endpoint, replaying the PKCE verifier and the resource indicator.
func exchangeMCPCode(ctx context.Context, tokenEndpoint, clientID, clientSecret, tokenAuthMethod, code, redirectURI, codeVerifier, resource string) (*mcpTokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("code_verifier", codeVerifier)
	form.Set("client_id", clientID)
	if resource != "" {
		form.Set("resource", resource)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	switch tokenAuthMethod {
	case "client_secret_basic":
		req.SetBasicAuth(url.QueryEscape(clientID), url.QueryEscape(clientSecret))
	case "client_secret_post":
		form.Set("client_secret", clientSecret)
		req.Body = io.NopCloser(strings.NewReader(form.Encode()))
		req.ContentLength = int64(len(form.Encode()))
	default:
		// "none" (public client + PKCE). If a secret was supplied without an
		// explicit method, include it as a post parameter defensively.
		if clientSecret != "" {
			form.Set("client_secret", clientSecret)
			req.Body = io.NopCloser(strings.NewReader(form.Encode()))
			req.ContentLength = int64(len(form.Encode()))
		}
	}

	resp, err := mcpOAuthHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed: status=%d body=%s", resp.StatusCode, truncate(string(body), 300))
	}
	var token mcpTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, fmt.Errorf("invalid token response: %w", err)
	}
	if token.AccessToken == "" {
		return nil, fmt.Errorf("token response did not include an access_token")
	}
	if token.TokenType == "" {
		token.TokenType = "Bearer"
	}
	return &token, nil
}

// generatePKCE returns a high-entropy code verifier and its S256 challenge
// (RFC 7636).
func generatePKCE() (verifier, challenge string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

// getJSON performs a bounded GET and decodes a JSON body. Non-2xx responses
// are reported as errors.
func getJSON(ctx context.Context, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := mcpOAuthHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GET %s returned status %d", endpoint, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
