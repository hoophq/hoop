// Authorization-server metadata mirror + RFC 7591 Dynamic Client Registration
// shim for IdPs without DCR support (JumpCloud, Okta org server, Entra ID).
//
// When a static OAuth client is configured (ServerMcpAuthConfig.ClientID), the
// RFC 9728 protected-resource document advertises this gateway as the
// authorization server. MCP clients then fetch RFC 8414 metadata from the
// gateway, which mirrors the upstream IdP's authorize/token/jwks endpoints but
// points registration_endpoint at a local shim that hands out the statically
// pre-registered client. The client subsequently talks to the IdP directly for
// the authorization code + PKCE flow; issued tokens are validated by the
// existing resource-server path (verifier.go) with the static client ID
// accepted as an alternate audience, since IdPs without RFC 8707 support mint
// tokens whose `aud` is the client ID rather than the resource URI.
package mcpauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/appconfig"
)

const (
	idpMetadataTTL     = 5 * time.Minute
	idpMetadataTimeout = 15 * time.Second
	// maxRegistrationBodyBytes bounds the unauthenticated RFC 7591 request
	// body; legitimate client metadata documents are well under this.
	maxRegistrationBodyBytes = 64 << 10
)

var idpMetadataHTTPClient = &http.Client{Timeout: idpMetadataTimeout}

// AuthorizationServerMetadata is the RFC 8414 document served by the gateway
// when a static MCP OAuth client is configured.
type AuthorizationServerMetadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	JwksURI                           string   `json:"jwks_uri,omitempty"`
	RegistrationEndpoint              string   `json:"registration_endpoint"`
	ScopesSupported                   []string `json:"scopes_supported,omitempty"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported"`
}

// idpAuthServerMetadata models the fields consumed from the upstream IdP's
// RFC 8414 / OIDC discovery document.
type idpAuthServerMetadata struct {
	Issuer                        string   `json:"issuer"`
	AuthorizationEndpoint         string   `json:"authorization_endpoint"`
	TokenEndpoint                 string   `json:"token_endpoint"`
	JwksURI                       string   `json:"jwks_uri"`
	ScopesSupported               []string `json:"scopes_supported"`
	ResponseTypesSupported        []string `json:"response_types_supported"`
	GrantTypesSupported           []string `json:"grant_types_supported"`
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported"`
}

// ClientRegistration is the RFC 7591 request/response shape. Only the fields
// MCP clients commonly send are modeled; the response echoes them back with
// the statically configured client credentials.
type ClientRegistration struct {
	ClientID                string   `json:"client_id,omitempty"`
	ClientSecret            string   `json:"client_secret,omitempty"`
	ClientSecretExpiresAt   *int64   `json:"client_secret_expires_at,omitempty"`
	RedirectURIs            []string `json:"redirect_uris,omitempty"`
	ClientName              string   `json:"client_name,omitempty"`
	GrantTypes              []string `json:"grant_types,omitempty"`
	ResponseTypes           []string `json:"response_types,omitempty"`
	Scope                   string   `json:"scope,omitempty"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method,omitempty"`
}

// authorizationServerIssuer is the external base URL of this gateway, used as
// the RFC 8414 issuer identifier of the metadata mirror. It must match the URL
// clients derived the well-known path from, so it includes the API URL path
// prefix when one is configured.
func authorizationServerIssuer() string {
	conf := appconfig.Get()
	return conf.ApiURL() + conf.ApiURLPath()
}

// RegistrationPath returns the gateway-relative path of the RFC 7591
// registration shim. Used by server.go to register the route.
func RegistrationPath() string {
	return appconfig.Get().ApiURLPath() + "/api/mcp/oauth/register"
}

// AuthorizationServerMetadataHandler serves the RFC 8414 authorization-server
// metadata document mirroring the configured IdP, with registration_endpoint
// pointing at the local DCR shim, so DCR-capable setups keep talking to the
// IdP directly.
//
//	@Summary		Get MCP OAuth Authorization Server Metadata
//	@Description	RFC 8414 authorization-server metadata mirroring the configured IdP, with registration_endpoint pointing at the gateway's RFC 7591 registration shim.
//	@Description	Served unauthenticated at the gateway root (outside the /api base path) as /.well-known/oauth-authorization-server and /.well-known/openid-configuration.
//	@Description	Returns 404 unless MCP OAuth is enabled with a static client configured; 502 when the IdP discovery document cannot be resolved.
//	@Tags			Server Management
//	@Produce		json
//	@Success		200		{object}	mcpauth.AuthorizationServerMetadata
//	@Failure		404,502	{object}	openapi.HTTPError
//	@Router			/.well-known/oauth-authorization-server [get]
func AuthorizationServerMetadataHandler(c *gin.Context) {
	cfg, ok := loadConfig()
	if !ok || !cfg.Enabled || cfg.ClientID == "" || cfg.IssuerURL == "" {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	idpMeta, err := fetchIdpMetadata(c.Request.Context(), cfg.IssuerURL)
	if err != nil {
		log.Warnf("mcp oauth: failed discovering IdP authorization server metadata, issuer=%s, reason=%v", cfg.IssuerURL, err)
		c.JSON(http.StatusBadGateway, gin.H{"message": "failed discovering identity provider metadata"})
		return
	}
	c.Header("Cache-Control", "public, max-age=300")
	c.JSON(http.StatusOK, buildAuthServerMetadata(cfg, idpMeta, authorizationServerIssuer(), appconfig.Get().ApiURL()+RegistrationPath()))
}

// buildAuthServerMetadata assembles the RFC 8414 mirror document: the
// gateway's own issuer identity, the IdP's endpoints, and the local
// registration shim. S256 is force-advertised since the MCP authorization
// profile requires it and supporting IdPs do not always list it.
//
// A configured scopes_supported replaces the IdP's own list: IdPs without RFC
// 8707 support advertise only their generic OIDC scopes, none of which belong
// to the MCP resource, so passing them through leads clients into an
// authorization request the IdP rejects.
func buildAuthServerMetadata(cfg effectiveConfig, idpMeta *idpAuthServerMetadata, issuer, registrationEndpoint string) AuthorizationServerMetadata {
	authMethods := []string{"none"}
	if cfg.ClientSecret != "" {
		authMethods = []string{"client_secret_basic", "client_secret_post"}
	}
	scopesSupported := idpMeta.ScopesSupported
	if len(cfg.ScopesSupported) > 0 {
		scopesSupported = cfg.ScopesSupported
	}
	return AuthorizationServerMetadata{
		Issuer:                            issuer,
		AuthorizationEndpoint:             idpMeta.AuthorizationEndpoint,
		TokenEndpoint:                     idpMeta.TokenEndpoint,
		JwksURI:                           idpMeta.JwksURI,
		RegistrationEndpoint:              registrationEndpoint,
		ScopesSupported:                   scopesSupported,
		ResponseTypesSupported:            defaultIfEmpty(idpMeta.ResponseTypesSupported, []string{"code"}),
		GrantTypesSupported:               defaultIfEmpty(idpMeta.GrantTypesSupported, []string{"authorization_code", "refresh_token"}),
		TokenEndpointAuthMethodsSupported: authMethods,
		CodeChallengeMethodsSupported:     ensureContains(idpMeta.CodeChallengeMethodsSupported, "S256"),
	}
}

// ClientRegistrationHandler is the RFC 7591 shim: it accepts any registration
// request and answers with the statically pre-registered client. The MCP
// client's redirect_uris are echoed back but NOT registered anywhere — the
// static client at the IdP must whitelist the client's callback URL for the
// authorization flow to complete.
//
// Note the endpoint is unauthenticated per RFC 7591, so a configured
// client_secret is disclosed to any caller. Public clients with PKCE (no
// secret) are the recommended configuration.
//
//	@Summary		Register MCP OAuth Client
//	@Description	RFC 7591 Dynamic Client Registration shim for IdPs without native DCR support: accepts any registration request and answers with the statically pre-registered OAuth client, echoing the caller's client metadata.
//	@Description	Served unauthenticated per RFC 7591. Returns 404 unless MCP OAuth is enabled with a static client configured.
//	@Tags			Server Management
//	@Accept			json
//	@Produce		json
//	@Param			request	body		mcpauth.ClientRegistration	true	"RFC 7591 client metadata"
//	@Success		201		{object}	mcpauth.ClientRegistration
//	@Failure		400,404	{object}	openapi.HTTPError
//	@Router			/mcp/oauth/register [post]
func ClientRegistrationHandler(c *gin.Context) {
	cfg, ok := loadConfig()
	if !ok || !cfg.Enabled || cfg.ClientID == "" {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	var req ClientRegistration
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":             "invalid_client_metadata",
			"error_description": err.Error(),
		})
		return
	}
	resp := buildRegistrationResponse(cfg, req)
	log.Infof("mcp oauth: DCR shim served static client, client_name=%q redirect_uris=%v", req.ClientName, req.RedirectURIs)
	c.JSON(http.StatusCreated, resp)
}

// buildRegistrationResponse shapes the RFC 7591 response: the static client
// with the caller's metadata echoed back. Public client (PKCE, auth method
// "none") unless a secret is configured; confidential clients honor a
// requested client_secret_post, defaulting to client_secret_basic, since the
// shim cannot know how the static client was registered at the IdP.
func buildRegistrationResponse(cfg effectiveConfig, req ClientRegistration) ClientRegistration {
	resp := ClientRegistration{
		ClientID:                cfg.ClientID,
		RedirectURIs:            req.RedirectURIs,
		ClientName:              req.ClientName,
		GrantTypes:              defaultIfEmpty(req.GrantTypes, []string{"authorization_code", "refresh_token"}),
		ResponseTypes:           defaultIfEmpty(req.ResponseTypes, []string{"code"}),
		Scope:                   req.Scope,
		TokenEndpointAuthMethod: "none",
	}
	if cfg.ClientSecret != "" {
		neverExpires := int64(0)
		resp.ClientSecret = cfg.ClientSecret
		resp.ClientSecretExpiresAt = &neverExpires
		resp.TokenEndpointAuthMethod = "client_secret_basic"
		if req.TokenEndpointAuthMethod == "client_secret_post" {
			resp.TokenEndpointAuthMethod = "client_secret_post"
		}
	}
	return resp
}

// idpMetadataCache memoizes the upstream IdP discovery document. A single
// entry suffices: the gateway has one configured issuer at a time.
var idpMetadataCache struct {
	mu        sync.Mutex
	issuer    string
	fetchedAt time.Time
	meta      *idpAuthServerMetadata
}

// fetchIdpMetadata resolves the IdP's discovery document, trying OIDC
// discovery first (universally served by the supported IdPs) and RFC 8414 as
// a fallback. Results are cached for idpMetadataTTL.
func fetchIdpMetadata(ctx context.Context, issuerURL string) (*idpAuthServerMetadata, error) {
	idpMetadataCache.mu.Lock()
	defer idpMetadataCache.mu.Unlock()
	if idpMetadataCache.meta != nil && idpMetadataCache.issuer == issuerURL &&
		time.Since(idpMetadataCache.fetchedAt) < idpMetadataTTL {
		return idpMetadataCache.meta, nil
	}
	base := strings.TrimSuffix(issuerURL, "/")
	candidates := []string{base + "/.well-known/openid-configuration"}
	// RFC 8414 §3.1: for issuers with a path component the well-known segment
	// is inserted between host and path. The suffix form is kept as a last
	// resort for servers that serve it non-standardly.
	if u, err := url.Parse(base); err == nil && u.EscapedPath() != "" && u.EscapedPath() != "/" {
		candidates = append(candidates, u.Scheme+"://"+u.Host+"/.well-known/oauth-authorization-server"+u.EscapedPath())
	}
	candidates = append(candidates, base+"/.well-known/oauth-authorization-server")
	var lastErr error
	for _, wellKnown := range candidates {
		meta, err := fetchAuthServerDocument(ctx, wellKnown)
		if err != nil {
			lastErr = err
			continue
		}
		if strings.TrimSuffix(meta.Issuer, "/") != base {
			lastErr = fmt.Errorf("issuer mismatch in discovery document, got=%q want=%q", meta.Issuer, issuerURL)
			continue
		}
		if meta.AuthorizationEndpoint == "" || meta.TokenEndpoint == "" {
			lastErr = fmt.Errorf("discovery document is missing authorization_endpoint or token_endpoint")
			continue
		}
		idpMetadataCache.issuer = issuerURL
		idpMetadataCache.fetchedAt = time.Now().UTC()
		idpMetadataCache.meta = meta
		return meta, nil
	}
	return nil, lastErr
}

func fetchAuthServerDocument(ctx context.Context, wellKnownURL string) (*idpAuthServerMetadata, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", wellKnownURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := idpMetadataHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s returned status %d", wellKnownURL, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var meta idpAuthServerMetadata
	if err := json.Unmarshal(body, &meta); err != nil {
		return nil, fmt.Errorf("failed decoding discovery document from %s: %w", wellKnownURL, err)
	}
	return &meta, nil
}

func defaultIfEmpty(v, def []string) []string {
	if len(v) == 0 {
		return def
	}
	return v
}

// ensureContains appends want when absent. The MCP authorization profile
// requires S256 PKCE support; IdPs that support it do not always advertise it.
func ensureContains(v []string, want string) []string {
	for _, s := range v {
		if s == want {
			return v
		}
	}
	return append(v, want)
}
