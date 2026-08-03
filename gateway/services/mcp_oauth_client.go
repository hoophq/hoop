// MCP OAuth client engine.
//
// The OAuth 2.1 client work Hoop performs on an admin's behalf against a
// remote MCP server: RFC 9728 protected-resource discovery, RFC 8414 / OIDC
// authorization-server discovery, RFC 7591 dynamic client registration, the
// authorization-code + PKCE request, code exchange, and refresh.
//
// The protocol comes from github.com/hoophq/mcpproxy/auth/outbound/oauth, which
// the gateway already depends on and which covers strictly more of the specs
// than a second in-tree implementation did: transport security on the metadata
// documents it fetches, S256 capability advertisement, structured RFC 6749
// §5.2 token errors, provider extras preserved, and the refresh grant this
// package needs. What stays here is the part that is Hoop's rather than the
// protocol's — which issuer to fall back to, which redirect URI to register,
// and how a hand-configured confidential client authenticates at the token
// endpoint.
//
// Two protocol checks live here rather than in the library because it does not
// make them: RFC 9728 §3.3 resource-identifier validation, and transport
// security on the endpoints a metadata document advertises (the library checks
// the URL it fetched the document from, not the URLs inside it — so an https
// document can hand back an http token endpoint).
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
//
// It refuses to follow redirects out of a POST. Every POST this package makes
// is credentialed — the token endpoint carries the client secret (Basic or in
// the body) and the refresh token, and registration carries the redirect URI
// Hoop will honor — and Go re-sends the body verbatim on a 307/308. A
// misconfigured or hostile authorization server could therefore bounce those
// credentials to a host of its choosing, over a scheme of its choosing.
// Refusing is the conservative choice over stripping: an authorization server
// that redirects its own token endpoint is broken in a way an admin should see
// as an error, not have papered over.
//
// Discovery GETs still follow redirects. They carry no credential, and
// .well-known documents served behind a redirect are common enough that
// refusing there would break working providers for no gain.
var mcpOAuthHTTPClient = &http.Client{
	Timeout: mcpDiscoveryTimeout,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		// via[0] is the original request: a 301/302/303 rewrites the method
		// to GET on the way here, so the current req.Method cannot tell us
		// what was sent.
		if len(via) > 0 && via[0].Method == http.MethodPost {
			return fmt.Errorf("refusing to follow a redirect from %s to %s: the request carries client credentials",
				via[0].URL.Redacted(), req.URL.Redacted())
		}
		// Same bound the stdlib applies when no policy is installed.
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	},
}

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
//
// Two checks the library does not make happen here: RFC 9728 §3.3 resource
// identifier validation (checkResourceIdentifier) and transport security on
// every endpoint the resulting flow will use (requireSecureEndpoint).
func DiscoverMCPAuthServer(ctx context.Context, serverURL string) (*MCPDiscovery, error) {
	resourceURL, err := url.Parse(serverURL)
	if err != nil || !resourceURL.IsAbs() || resourceURL.Host == "" {
		return nil, fmt.Errorf("server_url must be an absolute URL")
	}
	if resourceURL.Scheme != "https" && !oauth.IsLoopback(resourceURL.Hostname()) {
		return nil, fmt.Errorf("server_url must use https")
	}

	resource := strings.TrimSuffix(serverURL, "/")
	origin := fmt.Sprintf("%s://%s", resourceURL.Scheme, resourceURL.Host)
	var issuers, scopes []string

	if prm, err := oauth.FetchResourceMetadata(ctx, serverURL, mcpOAuthHTTPClient); err == nil && prm != nil {
		if err := checkResourceIdentifier(prm.Resource, resource, origin); err != nil {
			return nil, err
		}
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
		issuers = []string{origin}
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
	for _, endpoint := range []struct{ name, raw string }{
		{"authorization_endpoint", asMeta.AuthorizationEndpoint},
		{"token_endpoint", asMeta.TokenEndpoint},
		{"registration_endpoint", asMeta.RegistrationEndpoint},
	} {
		if err := requireSecureEndpoint(endpoint.name, endpoint.raw); err != nil {
			return nil, fmt.Errorf("authorization server %s: %w", issuer, err)
		}
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

// checkResourceIdentifier enforces RFC 9728 §3.3 on a protected-resource
// metadata document.
//
// §3.3: the returned "resource" MUST be identical to the resource identifier
// the well-known path suffix was inserted into to build the retrieval URL, and
// if it is not, the document MUST NOT be used. §6 defines "identical" as
// code-point equality — no Unicode normalization, no case folding, no URL
// canonicalization — so this is a plain string compare.
//
// Two identifiers are accepted because oauth.FetchResourceMetadata tries two
// retrieval URLs and does not report which one answered: the path-suffixed
// form derived from the full MCP endpoint (RFC 9728 §3.1, terminating slash
// removed) and the root form derived from its origin. Either is a resource
// identifier that legitimately produced the document; anything else is not.
//
// Without this a hostile MCP server can advertise a resource identifier that
// belongs to someone else, and Hoop would send that value as the RFC 8707
// resource indicator — minting a token audienced for a resource the caller
// never meant to reach and handing it to the server that asked. That is the
// impersonation attack §7.3 describes, and it is a confused deputy with the
// gateway as the deputy.
//
// An empty resource is tolerated: RFC 9728 makes the field REQUIRED, but the
// library already accepts a document that carries only authorization_servers,
// and rejecting one here would break MCP servers that publish that shape
// today. Nothing is adopted from it — the caller keeps the identifier it
// derived itself, which is the value §3.3 would have demanded anyway.
func checkResourceIdentifier(advertised, resourceIdentifier, origin string) error {
	if advertised == "" || advertised == resourceIdentifier || advertised == origin {
		return nil
	}
	return fmt.Errorf("the MCP server published protected-resource metadata for %q, which is not its own resource identifier (%q): "+
		"a resource may only describe itself (RFC 9728 §3.3)", advertised, resourceIdentifier)
}

// requireSecureEndpoint rejects a discovered OAuth endpoint that would carry
// credentials in the clear.
//
// The authorization request leaks nothing by itself, but the token and
// registration endpoints receive the client secret, the authorization code and
// the refresh token, and the authorization endpoint is where the browser is
// sent to authenticate — a plaintext one is a phishing surface. A discovered
// document is attacker-influenced input, so the scheme is checked here rather
// than trusted because the server URL was https.
//
// http:// is allowed on loopback hosts (127.0.0.1, ::1, localhost) because
// that is what running an MCP server on a laptop looks like, and loopback
// traffic never leaves the machine. Same rule the mcpproxy library applies to
// the documents themselves.
func requireSecureEndpoint(name, rawURL string) error {
	if rawURL == "" {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return fmt.Errorf("the %s is not an absolute URL: %q", name, rawURL)
	}
	if u.Scheme == "https" || (u.Scheme == "http" && oauth.IsLoopback(u.Host)) {
		return nil
	}
	return fmt.Errorf("the %s does not use https (%s); credentials sent there would travel in cleartext",
		name, u.Redacted())
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
//
// The endpoint is re-checked rather than trusted from discovery: this is also
// reached with a MCPDiscovery assembled from a stored flow row, which may
// predate the transport-security check in DiscoverMCPAuthServer.
func BuildMCPAuthorizationURL(d *MCPDiscovery, clientID, redirectURI, state, codeChallenge, scopes string) (string, error) {
	if err := requireSecureEndpoint("authorization_endpoint", d.AuthorizationEndpoint); err != nil {
		return "", err
	}
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
//
// The token endpoint arrives from a stored flow row, so it is re-checked for
// transport security here: a row written before that check existed would
// otherwise put the authorization code and the client secret on the wire in
// cleartext.
func ExchangeMCPCode(ctx context.Context, d *MCPDiscovery, clientID, clientSecret, tokenAuthMethod, code, redirectURI, codeVerifier string) (*outbound.Token, error) {
	if err := requireSecureEndpoint("token_endpoint", d.TokenEndpoint); err != nil {
		return nil, err
	}
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
//
// Both branches guarantee a non-empty RefreshToken on success. RFC 6749 §6
// makes refresh_token OPTIONAL in the response and a server that does not
// rotate simply omits it, meaning "keep the one you have"; the caller persists
// what comes back, so the carry-forward has to happen before it gets there.
// oauth.Refresh does this too, but relying on that would put the invariant in
// a dependency while the erase it prevents happens here.
func refreshMCPToken(ctx context.Context, cfg oauth.ClientConfig, tokenAuthMethod, refreshToken string) (*outbound.Token, error) {
	if refreshToken == "" {
		return nil, errors.New("refresh called with an empty refresh token")
	}
	tok, err := postRefreshGrant(ctx, cfg, tokenAuthMethod, refreshToken)
	if err != nil {
		return nil, err
	}
	if tok.RefreshToken == "" {
		tok.RefreshToken = refreshToken
	}
	return tok, nil
}

// postRefreshGrant sends the refresh-token request, choosing where the client
// credentials travel. It returns the token endpoint's answer verbatim.
//
// The endpoint is read off a grant row written at login time, so it is
// re-checked for transport security: this call carries the refresh token and
// the client secret, and a grant stored before the check existed would send
// both in cleartext at every session open.
func postRefreshGrant(ctx context.Context, cfg oauth.ClientConfig, tokenAuthMethod, refreshToken string) (*outbound.Token, error) {
	if err := requireSecureEndpoint("token_endpoint", cfg.TokenEndpoint); err != nil {
		return nil, err
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
	return postSecretInBody(ctx, cfg, form)
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
