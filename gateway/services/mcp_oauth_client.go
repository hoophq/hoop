// MCP OAuth client engine.
//
// The OAuth 2.1 client work Hoop performs on an admin's behalf against a
// remote MCP server: RFC 9728 protected-resource discovery, RFC 8414 / OIDC
// authorization-server discovery, RFC 7591 dynamic client registration, the
// authorization-code + PKCE request, code exchange, and refresh.
//
// The protocol comes from github.com/hoophq/mcpproxy/auth/outbound/oauth, which
// the gateway already depends on and which covers strictly more of the specs
// than a second in-tree implementation did: transport-security checks on
// discovered endpoints, S256 capability advertisement, structured RFC 6749 §5.2
// token errors, provider extras preserved, and the refresh grant this package
// needs. What stays here is the part that is Hoop's rather than the protocol's
// — which issuer to fall back to, which redirect URI to register, and how a
// hand-configured confidential client authenticates at the token endpoint.
//
// It lives in services rather than beside the HTTP handlers because both the
// handlers (api/connections) and the session-open grant refresh
// (mcp_oauth_grant.go) drive it, and the handlers already depend on services.
package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hoophq/mcpproxy/auth/outbound"
	"github.com/hoophq/mcpproxy/auth/outbound/oauth"
)

// mcpDiscoveryTimeout bounds each outbound discovery / registration / token
// request to the upstream MCP authorization server.
const mcpDiscoveryTimeout = 15 * time.Second

// mcpOAuthHTTPClient is the bounded client used for every upstream OAuth call.
var mcpOAuthHTTPClient = &http.Client{Timeout: mcpDiscoveryTimeout}

// MCPDiscovery is the resolved set of endpoints Hoop needs to drive an
// authorization-code flow against one MCP server.
type MCPDiscovery struct {
	Resource              string
	Issuer                string
	AuthorizationEndpoint string
	TokenEndpoint         string
	RegistrationEndpoint  string
	ScopesSupported       []string
}

// MCPClientRegistration holds the credentials a dynamic registration produced,
// or the ones an admin supplied directly.
type MCPClientRegistration struct {
	ClientID        string
	ClientSecret    string
	TokenAuthMethod string
}

// DiscoverMCPAuthServer resolves the authorization server endpoints for an MCP
// server URL.
//
// The MCP endpoint (the OAuth "resource") advertises its authorization servers
// through RFC 9728 protected-resource metadata. Two Hoop-specific behaviors sit
// on top of the library's discovery:
//
//   - Every advertised authorization server is tried in order, not just the
//     first, so a resource listing a stale issuer ahead of a live one still
//     works.
//   - When no protected-resource metadata is published at all, the server URL's
//     origin is treated as the issuer. The library errors instead; Hoop keeps
//     the fallback because servers that co-locate the resource and the
//     authorization server are common and were supported before.
func DiscoverMCPAuthServer(ctx context.Context, serverURL string) (*MCPDiscovery, error) {
	resourceURL, err := url.Parse(serverURL)
	if err != nil || !resourceURL.IsAbs() || resourceURL.Host == "" {
		return nil, fmt.Errorf("server_url must be an absolute URL")
	}
	if resourceURL.Scheme != "https" && !oauth.IsLoopback(resourceURL.Hostname()) {
		return nil, fmt.Errorf("server_url must use https")
	}

	resource := strings.TrimSuffix(serverURL, "/")
	var issuers, scopes []string

	if prm, err := oauth.FetchResourceMetadata(ctx, serverURL, mcpOAuthHTTPClient); err == nil && prm != nil {
		for _, as := range prm.AuthorizationServers {
			if as = strings.TrimSpace(as); as != "" {
				issuers = append(issuers, as)
			}
		}
		if prm.Resource != "" {
			resource = prm.Resource
		}
		scopes = prm.ScopesSupported
	}
	if len(issuers) == 0 {
		issuers = []string{fmt.Sprintf("%s://%s", resourceURL.Scheme, resourceURL.Host)}
	}

	var asMeta *oauth.ASMetadata
	var issuer string
	var lastErr error
	for _, candidate := range issuers {
		md, err := oauth.FetchASMetadata(ctx, candidate, mcpOAuthHTTPClient)
		if err != nil {
			lastErr = err
			continue
		}
		if md.AuthorizationEndpoint == "" {
			lastErr = fmt.Errorf("authorization server %s published no authorization_endpoint", candidate)
			continue
		}
		asMeta, issuer = md, candidate
		break
	}
	if asMeta == nil {
		return nil, fmt.Errorf("failed discovering authorization server metadata: %w", lastErr)
	}
	if !asMeta.SupportsS256() {
		return nil, fmt.Errorf("authorization server %s does not support PKCE S256, which the MCP authorization profile requires", issuer)
	}
	if len(scopes) == 0 {
		scopes = asMeta.ScopesSupported
	}

	return &MCPDiscovery{
		Resource:              resource,
		Issuer:                strings.TrimRight(issuer, "/"),
		AuthorizationEndpoint: asMeta.AuthorizationEndpoint,
		TokenEndpoint:         asMeta.TokenEndpoint,
		RegistrationEndpoint:  asMeta.RegistrationEndpoint,
		ScopesSupported:       scopes,
	}, nil
}

// RegisterMCPClient performs RFC 7591 dynamic client registration, registering
// Hoop's gateway callback as the redirect URI.
//
// Hoop registers a public client: the authorization code is bound by PKCE and
// the redirect lands on the gateway, so a client secret would add a stored
// secret without adding a check. A server that issues one anyway is honored —
// the returned auth method reflects what it decided, not what was asked for.
func RegisterMCPClient(ctx context.Context, registrationEndpoint, redirectURI string, scopes []string) (*MCPClientRegistration, error) {
	if registrationEndpoint == "" {
		// Plenty of authorization servers never implemented DCR — GitHub's
		// publishes no registration_endpoint — so this is a routine outcome,
		// not a broken server, and the message has to be actionable. It names
		// the redirect URI because that is the value an admin must paste into
		// the provider, and it does not demand a secret: an OAuth app used
		// with PKCE authenticates by client_id alone, and asking for both
		// sends admins hunting a value the provider may never have issued.
		return nil, fmt.Errorf("this authorization server does not support dynamic client registration; "+
			"create an OAuth app with %s as its redirect URI, then enter its Client ID "+
			"(and Client Secret, only if the provider issued one)", redirectURI)
	}
	client, err := oauth.RegisterClient(ctx, registrationEndpoint, []string{redirectURI}, scopes, oauth.RegisterOptions{
		ClientName: "Hoop",
		HTTPClient: mcpOAuthHTTPClient,
	})
	if err != nil {
		return nil, err
	}
	method := client.AuthMethod
	if method == "" {
		method = tokenAuthMethodNone
		if client.ClientSecret != "" {
			method = tokenAuthMethodClientSecretPost
		}
	}
	return &MCPClientRegistration{
		ClientID:        client.ClientID,
		ClientSecret:    client.ClientSecret,
		TokenAuthMethod: method,
	}, nil
}

// BuildMCPAuthorizationURL constructs the authorization-code request URL with
// an S256 PKCE challenge and the RFC 8707 resource indicator.
func BuildMCPAuthorizationURL(d *MCPDiscovery, clientID, redirectURI, state, codeChallenge, scopes string) (string, error) {
	return oauth.BuildAuthorizeURL(oauth.ClientConfig{
		ClientID:     clientID,
		AuthEndpoint: d.AuthorizationEndpoint,
		Audience:     d.Resource,
		Scopes:       strings.Fields(scopes),
	}, redirectURI, state, codeChallenge)
}

// GenerateMCPPKCE returns a code verifier and its S256 challenge (RFC 7636).
//
// The library generates these inside its own all-in-one flow and does not
// export them, because that flow assumes one blocking call. Hoop drives
// authorize and callback as separate HTTP requests minutes apart and must park
// the verifier in the database between them, so it owns the generation.
func GenerateMCPPKCE() (verifier, challenge string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

// ExchangeMCPCode redeems an authorization code at the token endpoint,
// replaying the PKCE verifier and the resource indicator.
func ExchangeMCPCode(ctx context.Context, d *MCPDiscovery, clientID, clientSecret, tokenAuthMethod, code, redirectURI, codeVerifier string) (*outbound.Token, error) {
	cfg := oauth.ClientConfig{
		ClientID:      clientID,
		ClientSecret:  clientSecret,
		TokenEndpoint: d.TokenEndpoint,
		Audience:      d.Resource,
		HTTPClient:    mcpOAuthHTTPClient,
	}
	if usesSecretPost(tokenAuthMethod, clientSecret) {
		return postSecretInBody(ctx, cfg, url.Values{
			"grant_type":    {"authorization_code"},
			"code":          {code},
			"redirect_uri":  {redirectURI},
			"code_verifier": {codeVerifier},
		})
	}
	return oauth.ExchangeCode(ctx, cfg, code, redirectURI, codeVerifier)
}

// refreshMCPToken runs the refresh-token grant for a stored grant.
func refreshMCPToken(ctx context.Context, cfg oauth.ClientConfig, tokenAuthMethod, refreshToken string) (*outbound.Token, error) {
	if refreshToken == "" {
		return nil, errors.New("refresh called with an empty refresh token")
	}
	if !usesSecretPost(tokenAuthMethod, cfg.ClientSecret) {
		return oauth.Refresh(ctx, cfg, refreshToken)
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	}
	if len(cfg.Scopes) > 0 {
		form.Set("scope", strings.Join(cfg.Scopes, " "))
	}
	tok, err := postSecretInBody(ctx, cfg, form)
	if err != nil {
		return nil, err
	}
	// A server that does not rotate refresh tokens omits the field; dropping
	// it would make the next refresh impossible.
	if tok.RefreshToken == "" {
		tok.RefreshToken = refreshToken
	}
	return tok, nil
}

// Token-endpoint authentication methods Hoop records on a flow or grant.
const (
	tokenAuthMethodNone             = "none"
	tokenAuthMethodClientSecretPost = "client_secret_post"
)

// usesSecretPost reports whether the client credentials must travel in the
// request body rather than as HTTP Basic.
//
// oauth.postToken sends Basic whenever a secret is present — the RFC 6749
// §2.3.1 method every server must support, and the right default. Hoop also
// records client_secret_post for clients an admin configured by hand, and some
// authorization servers accept only that. Silently promoting those to Basic
// would break connections that work today, so this one case keeps a Hoop-side
// request builder.
func usesSecretPost(tokenAuthMethod, clientSecret string) bool {
	return clientSecret != "" && tokenAuthMethod == tokenAuthMethodClientSecretPost
}

// tokenEndpointResponse is the RFC 6749 §5.1 success body and §5.2 error body.
// Only the fields Hoop persists are modeled: unlike the library's decoder this
// path drops provider extras, which no caller of it reads.
type tokenEndpointResponse struct {
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	ExpiresIn        int64  `json:"expires_in"`
	RefreshToken     string `json:"refresh_token"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// postSecretInBody performs a token-endpoint request carrying the client
// credentials in the form body.
//
// Errors come back as *oauth.TokenError so callers can distinguish a dead
// grant (invalid_grant) from a transient failure on this path exactly as they
// can on the library's.
func postSecretInBody(ctx context.Context, cfg oauth.ClientConfig, form url.Values) (*outbound.Token, error) {
	if cfg.TokenEndpoint == "" {
		return nil, errors.New("token endpoint is empty")
	}
	form.Set("client_id", cfg.ClientID)
	form.Set("client_secret", cfg.ClientSecret)
	if cfg.Audience != "" {
		form.Set("resource", cfg.Audience)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	hc := cfg.HTTPClient
	if hc == nil {
		hc = mcpOAuthHTTPClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read token response: %w", err)
	}

	var body tokenEndpointResponse
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("decode token response (status %d): %w", resp.StatusCode, err)
	}
	if body.Error != "" {
		return nil, &oauth.TokenError{Code: body.Error, Description: body.ErrorDescription, Status: resp.StatusCode}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &oauth.TokenError{Code: "http_error", Description: fmt.Sprintf("status %d", resp.StatusCode), Status: resp.StatusCode}
	}
	if body.AccessToken == "" {
		return nil, errors.New("token response contained no access_token")
	}

	tok := &outbound.Token{
		AccessToken:  body.AccessToken,
		RefreshToken: body.RefreshToken,
		TokenType:    body.TokenType,
	}
	if body.ExpiresIn > 0 {
		tok.Expiry = time.Now().Add(time.Duration(body.ExpiresIn) * time.Second)
	}
	return tok, nil
}
