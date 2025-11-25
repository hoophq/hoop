package apiroutes

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/apiutils"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/idp"
	idptypes "github.com/hoophq/hoop/gateway/idp/types"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
)

func (r *Router) loadTokenVerifier(c *gin.Context) (idp.UserInfoTokenVerifier, idptypes.ServerConfig, bool) {
	tokenVerifier, serverConfig, err := idp.NewUserInfoTokenVerifierProvider()
	switch err {
	case idp.ErrUnknownIdpProvider:
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"message": "unknown idp provider"})
		return nil, idptypes.ServerConfig{}, false
	case nil:
	default:
		log.Errorf("failed to load IDP provider: %v", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "internal server error, failed loading IDP provider"})
		return nil, idptypes.ServerConfig{}, false
	}
	return tokenVerifier, serverConfig, err == nil
}

func (r *Router) AuthMiddleware(c *gin.Context) {
	tokenVerifier, serverConfig, ok := r.loadTokenVerifier(c)
	if !ok {
		return
	}

	// api key authentication validation
	// allow accessing all routes as admin
	if apiKey := c.GetHeader("Api-Key"); apiKey != "" {
		registeredApiKey := serverConfig.ApiKey
		if registeredApiKey == "" || appconfig.Get().OrgMultitenant() {
			log.Warnf("api key is not set or configured in the server")
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "access denied"})
			return
		}

		if registeredApiKey != apiKey {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "unauthorize"})
			return
		}

		// legacy api key format: orgID|key
		orgID := strings.Split(apiKey, "|")[0]
		if strings.HasPrefix(apiKey, "xapi-") {
			// the new format is derived from the server config database
			orgID = serverConfig.OrgID
		}

		deterministicUuid := uuid.NewSHA1(uuid.NameSpaceURL, []byte(`API_KEY`))
		r.setUserContext(&models.Context{
			OrgID:          orgID,
			OrgName:        proto.DefaultOrgName,
			OrgLicenseData: nil, // not enforcing license for API keys at this moment
			UserID:         deterministicUuid.String(),
			UserSubject:    "API_KEY",
			UserName:       "API_KEY",
			UserEmail:      "API_KEY",
			UserStatus:     "active",
			UserGroups:     []string{types.GroupAdmin},
		}, c, serverConfig.GrpcURL, serverConfig.AuthMethod)
		return
	}

	// jwt key authentication
	subject, err := r.validateAccessToken(tokenVerifier, c)
	if err != nil {
		tokenHeader := c.GetHeader("authorization")
		log.Infof("failed authenticating, %v, length=%v, reason=%v, url-path=%v",
			parseHeaderForDebug(tokenHeader), len(tokenHeader), err, c.Request.URL.Path)
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "access denied"})
		return
	}

	ctx, err := models.GetUserContext(subject)
	if err != nil {
		log.Errorf("failed fetching user, subject=%v, err=%v", subject, err)
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "access denied"})
		return
	}
	if ctx.UserStatus != string(types.UserStatusActive) {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "access denied"})
		return
	}

	// it's an unregistered user, validate it via user info
	routeType := routeTypeFromContext(c)
	if routeType == routeUserInfoType && ctx.IsEmpty() {
		uinfo, err := r.validateTokenWithUserInfo(tokenVerifier, c)
		if err != nil {
			log.Warnf("failed authenticating anonymous user via userinfo endpoint, err=%v", err)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "access denied"})
			return
		}

		if uinfo.Email == "" || uinfo.Subject == "" {
			log.Warnf("failed authenticating unregistered user via userinfo endpoint, email=(set=%v), subject=(set=%v)",
				uinfo.Email != "", uinfo.Subject != "")
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "access denied"})
			return
		}
		c.Set(storagev2.ContextKey,
			storagev2.NewContext("", "").
				WithAnonymousInfo(uinfo.Profile, uinfo.Email, uinfo.Subject, uinfo.Picture, uinfo.EmailVerified).
				WithApiURL(r.apiURL).
				WithGrpcURL(serverConfig.GrpcURL).
				WithProviderType(serverConfig.AuthMethod),
		)
		c.Next()
		return
	}

	// from this point forward, the user must be authenticated and registered in the database.
	if ctx.IsEmpty() {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "access denied"})
		return
	}

	// validate routes based on permissions from the user groups of a registered user
	roles := rolesFromContext(c)
	if !isGroupAllowed(ctx.UserGroups, roles...) {
		log.Debugf("forbidden access: %v %v, roles=%v, email=%v",
			c.Request.Method, c.Request.URL.Path, roles, ctx.UserEmail)
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"message": "access denied"})
		return
	}

	log.Debugf("access allowed: %v %v, roles=%v, email=%s, isadmin=%v, isauditor=%v",
		c.Request.Method, c.Request.URL.Path, roles, ctx.UserEmail, ctx.IsAdmin(), ctx.IsAuditor())
	r.setUserContext(ctx, c, serverConfig.GrpcURL, serverConfig.AuthMethod)
}

// setUserContext and call next middleware
func (r *Router) setUserContext(ctx *models.Context, c *gin.Context, grpcURL string, providerType idptypes.ProviderType) {
	auditApiChanges(c, ctx)
	c.Set(storagev2.ContextKey,
		storagev2.NewContext(ctx.UserSubject, ctx.OrgID).
			WithUserInfo(ctx.UserName, ctx.UserEmail, string(ctx.UserStatus), ctx.UserPicture, ctx.UserGroups).
			WithSlackID(ctx.UserSlackID).
			WithOrgName(ctx.OrgName).
			WithOrgLicenseData(ctx.OrgLicenseData).
			WithApiURL(r.apiURL).
			WithGrpcURL(grpcURL).
			WithProviderType(providerType),
	)

	token, err := parseToken(c)
	if err != nil {
		log.Errorf("failed parsing token for storing in user tokens map, reason=%v", err)
	}
	// if already there skip the add
	_, ok := idp.UserTokens.Load(token)
	if !ok {
		idp.UserTokens.Store(ctx.UserSubject, token)
	}
	c.Next()
}

// validateToken validates the access token by the user info if it's an opaque token
// or by parsing and validating the token if it's a JWT token
func (r *Router) validateAccessToken(tokenVerifier idp.TokenVerifier, c *gin.Context) (string, error) {
	token, err := parseToken(c)
	if err != nil {
		return "", err
	}
	return tokenVerifier.VerifyAccessToken(token)
}

// validateTokenWithUserInfo validates the access token by the user info endpoint
func (r *Router) validateTokenWithUserInfo(tokenVerifier idp.UserInfoTokenVerifier, c *gin.Context) (*idptypes.ProviderUserInfo, error) {
	accessToken, err := parseToken(c)
	if err != nil {
		return nil, err
	}
	return tokenVerifier.VerifyAccessTokenWithUserInfo(accessToken)
}

func parseToken(c *gin.Context) (string, error) {
	tokenHeader := c.GetHeader("authorization")
	tokenParts := strings.Split(tokenHeader, " ")
	if len(tokenParts) != 2 || tokenParts[0] != "Bearer" || tokenParts[1] == "" {
		return "", errors.New("invalid authorization header")
	}
	return tokenParts[1], nil
}

func parseHeaderForDebug(authTokenHeader string) string {
	prefixAuthHeader := "N/A"
	if len(authTokenHeader) > 18 {
		prefixAuthHeader = authTokenHeader[0:18]
	}
	bearerString, token, found := strings.Cut(authTokenHeader, " ")
	if !found || bearerString != "Bearer" {
		return fmt.Sprintf("isjwt=unknown, prefix-auth-header[19]=%v", prefixAuthHeader)
	}
	header, payload, found := strings.Cut(token, ".")
	if !found {
		return fmt.Sprintf("isjwt=false, prefix-auth-header[19]=%v", prefixAuthHeader)
	}
	headerBytes, _ := base64.StdEncoding.DecodeString(header)
	payloadBytes, _ := base64.StdEncoding.DecodeString(payload)
	headerBytes = bytes.ReplaceAll(headerBytes, []byte(`"`), []byte(`'`))
	payloadBytes = bytes.ReplaceAll(payloadBytes, []byte(`"`), []byte(`'`))
	return fmt.Sprintf("isjwt=true, header=%v, payload=%v", string(headerBytes), string(payloadBytes))
}

func auditApiChanges(c *gin.Context, ctx *models.Context) {
	if c.Request.Method == "GET" || c.Request.Method == "HEAD" {
		return
	}
	log.With(
		"email", ctx.UserEmail,
		"org", ctx.OrgName,
		"method", c.Request.Method,
		"path", c.Request.URL.Path,
		"user-agent", apiutils.NormalizeUserAgent(c.Request.Header.Values),
		"content-length", c.Request.ContentLength,
	).Info("api-audit")
}

// GetAccessTokenFromRequest extracts the access token from the request headers.
func GetAccessTokenFromRequest(c *gin.Context) string {
	tokenHeader := c.GetHeader("authorization")
	apiKey := c.GetHeader("Api-Key")
	if apiKey != "" {
		return apiKey
	}
	tokenParts := strings.Split(tokenHeader, " ")
	if len(tokenParts) > 1 {
		return tokenParts[1]
	}
	return ""
}
