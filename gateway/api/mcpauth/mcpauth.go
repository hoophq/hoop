// Package mcpauth implements OAuth 2.1 Resource Server semantics for the Hoop
// MCP endpoint per the Model Context Protocol 2025-11-25 authorization profile.
//
// It exposes the RFC 9728 protected-resource metadata document and a Gin
// middleware that strictly validates IdP-issued JWTs (RFC 8707 audience
// binding, RFC 6750 WWW-Authenticate challenges) and falls back to the legacy
// Hoop bearer-token middleware when MCP OAuth is disabled or the incoming
// token is a Hoop-issued web token.
//
// Per the MCP spec, the IdP JWT is consumed at the gateway and never forwarded
// to downstream backend connections.
package mcpauth

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/appconfig"
)

const defaultGroupsClaim = "groups"

type LegacyAuthMiddleware func(c *gin.Context)

// Middleware returns a Gin handler that authenticates MCP requests. When MCP
// OAuth is disabled for the current org, or when the bearer token is not a
// JWT, the legacy Hoop auth path is used unchanged. JWT bearers are validated
// against the configured OIDC issuer with strict audience binding.
func Middleware(legacyAuth LegacyAuthMiddleware) gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg, ok := loadConfig()
		log.Debugf("mcp auth: method=%s path=%s hasAuth=%v cfgOk=%v enabled=%v issuer=%s resource=%s",
			c.Request.Method, c.Request.URL.Path, c.GetHeader("authorization") != "",
			ok, cfg.Enabled, cfg.IssuerURL, cfg.ResourceURI)
		if !ok || !cfg.Enabled {
			legacyAuth(c)
			return
		}
		bearer := extractBearer(c)
		if bearer == "" {
			log.Debugf("mcp auth: no bearer, emitting challenge")
			writeChallenge(c, "missing or malformed Authorization header", "invalid_request")
			return
		}
		if !isFederatedJWT(bearer, cfg.IssuerURL) {
			log.Debugf("mcp auth: bearer issuer does not match, routing to legacy")
			legacyAuth(c)
			return
		}
		log.Debugf("mcp auth: validating oauth 2.1 jwt against issuer=%s audience=%s", cfg.IssuerURL, cfg.ResourceURI)
		if err := authenticateOAuth21(c, bearer, cfg); err != nil {
			log.Warnf("mcp auth: token rejected, reason=%v", err)
			writeChallenge(c, err.Error(), "invalid_token")
			return
		}
		log.Debugf("mcp auth: oauth 2.1 validation succeeded, forwarding to mcp server")
	}
}

// MetadataHandler serves the RFC 9728 protected-resource metadata document.
// Returns 404 when MCP OAuth is disabled so clients fall back to the
// gateway's legacy bearer-token flow.
func MetadataHandler(c *gin.Context) {
	cfg, ok := loadConfig()
	if !ok || !cfg.Enabled {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	if cfg.IssuerURL == "" {
		log.Warnf("mcp oauth: well-known requested but issuer URL is empty")
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	c.Header("Cache-Control", "public, max-age=3600")
	c.JSON(http.StatusOK, protectedResourceMetadata{
		Resource:               cfg.ResourceURI,
		AuthorizationServers:   []string{cfg.IssuerURL},
		BearerMethodsSupported: []string{"header"},
		ResourceName:           "Hoop MCP",
		ResourceDocumentation:  "https://hoop.dev/docs/mcp/auth",
	})
}

// McpResourcePath returns the path component of the MCP route relative to the
// gateway host root (e.g. "/api/mcp" or "/<api-prefix>/api/mcp"). Used by
// server.go to register the RFC 9728 §3.1 path-suffixed well-known route.
func McpResourcePath() string {
	return appconfig.Get().ApiURLPath() + "/api/mcp"
}

// protectedResourceMetadata is the JSON shape defined by RFC 9728 §2 and
// referenced by the MCP 2025-11-25 authorization spec.
type protectedResourceMetadata struct {
	Resource               string   `json:"resource"`
	AuthorizationServers   []string `json:"authorization_servers"`
	BearerMethodsSupported []string `json:"bearer_methods_supported"`
	ScopesSupported        []string `json:"scopes_supported,omitempty"`
	ResourceName           string   `json:"resource_name,omitempty"`
	ResourceDocumentation  string   `json:"resource_documentation,omitempty"`
}

func extractBearer(c *gin.Context) string {
	parts := strings.SplitN(c.GetHeader("authorization"), " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

// isFederatedJWT reports whether the bearer should be routed through the OAuth
// 2.1 validation path rather than the legacy Hoop middleware. It peeks at the
// JWT's `iss` claim WITHOUT verifying the signature and returns true only when
// the issuer matches the configured IdP issuer. Hoop-issued tokens (same JWT
// shape but signed with a different key) fall through to legacy auth so they
// are not rejected by the JWKS keyfunc during dual-accept rollout.
//
// Unsafe JSON parsing is acceptable here because the decision only affects
// routing; the actual signature + iss + aud checks still happen inside
// VerifyAccessTokenForResource before any trust is extended to the claims.
func isFederatedJWT(token, expectedIssuer string) bool {
	if expectedIssuer == "" {
		return false
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		if payload, err = base64.StdEncoding.DecodeString(parts[1]); err != nil {
			return false
		}
	}
	var claims struct {
		Iss string `json:"iss"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return false
	}
	return strings.TrimRight(claims.Iss, "/") == strings.TrimRight(expectedIssuer, "/")
}

func writeChallenge(c *gin.Context, errorDesc, errorCode string) {
	wellKnown := appconfig.Get().ApiURL() + "/.well-known/oauth-protected-resource" + McpResourcePath()
	challenge := `Bearer resource_metadata="` + wellKnown + `"`
	if errorCode != "" {
		challenge += `, error="` + errorCode + `"`
	}
	if errorDesc != "" {
		challenge += `, error_description="` + sanitizeChallenge(errorDesc) + `"`
	}
	c.Header("WWW-Authenticate", challenge)
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "access denied"})
}

// sanitizeChallenge prevents WWW-Authenticate header injection by stripping
// CR/LF and double quotes. RFC 6750 §3 only permits a restricted character set.
func sanitizeChallenge(s string) string {
	r := strings.NewReplacer("\r", " ", "\n", " ", `"`, "'")
	return r.Replace(s)
}
