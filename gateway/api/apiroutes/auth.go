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
	"github.com/hoophq/hoop/gateway/pgrest"
	pgorgs "github.com/hoophq/hoop/gateway/pgrest/orgs"
	pguserauth "github.com/hoophq/hoop/gateway/pgrest/userauth"
	"github.com/hoophq/hoop/gateway/security/idp"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
)

func (r *Router) AuthMiddleware(c *gin.Context) {
	// api key authentication validation
	// allow accessing all routes as admin
	if apiKey := c.GetHeader("Api-Key"); apiKey != "" {
		if r.registeredApiKey != apiKey {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "unauthorize"})
			return
		}
		orgID := strings.Split(apiKey, "|")[0]
		newOrgCtx := pgrest.NewOrgContext(orgID)
		org, err := pgorgs.New().FetchOrgByContext(newOrgCtx)
		if err != nil || org == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "unauthorized"})
			return
		}

		deterministicUuid := uuid.NewSHA1(uuid.NameSpaceURL, []byte(`API_KEY`))
		r.setUserContext(&pguserauth.Context{
			OrgID:       orgID,
			OrgName:     org.Name,
			OrgLicense:  org.License,
			UserUUID:    deterministicUuid.String(),
			UserSubject: "API_KEY",
			UserName:    "API_KEY",
			UserEmail:   "API_KEY",
			UserStatus:  "active",
			UserGroups:  []string{types.GroupAdmin},
		}, c)
		return
	}

	// jwt key authentication
	subject, err := r.validateAccessToken(c)
	if err != nil {
		tokenHeader := c.GetHeader("authorization")
		log.Infof("failed authenticating, %v, length=%v, reason=%v, url-path=%v",
			parseHeaderForDebug(tokenHeader), len(tokenHeader), err, c.Request.URL.Path)
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "unauthorized"})
		return
	}

	ctx, err := pguserauth.New().FetchUserContext(subject)
	if err != nil {
		log.Errorf("failed fetching user, subject=%v, err=%v", subject, err)
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "unauthorized"})
		return
	}
	if ctx.UserStatus != string(types.UserStatusActive) {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "unauthorized"})
		return
	}

	// it's an unregistered user, validate it via user info
	routeType := routeTypeFromContext(c)
	if routeType == routeUserInfoType && ctx.IsEmpty() {
		uinfo, err := r.validateTokenWithUserInfo(c)
		if err != nil {
			log.Warnf("failed authenticating anonymous user via userinfo endpoint, err=%v", err)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "unauthorized"})
			return
		}

		if uinfo.Email == "" || uinfo.Subject == "" {
			log.Warnf("failed authenticating unregistered user via userinfo endpoint, email=(set=%v), subject=(set=%v)",
				uinfo.Email != "", uinfo.Subject != "")
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "unauthorized"})
			return
		}
		c.Set(storagev2.ContextKey,
			storagev2.NewContext("", "").
				WithAnonymousInfo(uinfo.Profile, uinfo.Email, uinfo.Subject, uinfo.Picture, uinfo.EmailVerified).
				WithApiURL(r.provider.ApiURL).
				WithGrpcURL(r.grpcURL),
		)
		c.Next()
		return
	}

	// from this point forward, the user must be authenticated and registered in the database.
	if ctx.IsEmpty() {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "unauthorized"})
		return
	}

	// validate routes based on permissions from the user groups of a registered user
	roles := rolesFromContext(c)
	if !isGroupAllowed(ctx.UserGroups, roles...) {
		log.Debugf("not allowed to access route, user=%v, path=%v, roles=%v",
			ctx.UserEmail, c.Request.URL.Path, roles)
		c.AbortWithStatus(http.StatusForbidden)
		return
	}

	log.Debugf("user authenticated, roles=%v, org=%s, subject=%s, isadmin=%v, isauditor=%v",
		roles, ctx.OrgName, subject, ctx.IsAdmin(), ctx.IsAuditor())
	r.setUserContext(ctx, c)
}

// setUserContext and call next middleware
func (r *Router) setUserContext(ctx *pguserauth.Context, c *gin.Context) {
	auditApiChanges(c, ctx)
	c.Set(storagev2.ContextKey,
		storagev2.NewContext(ctx.UserSubject, ctx.OrgID).
			WithUserInfo(ctx.UserName, ctx.UserEmail, string(ctx.UserStatus), ctx.UserPicture, ctx.UserGroups).
			WithSlackID(ctx.UserSlackID).
			WithOrgName(ctx.OrgName).
			WithOrgLicenseData(ctx.OrgLicenseData).
			WithApiURL(r.provider.ApiURL).
			WithGrpcURL(r.grpcURL),
	)
	c.Next()
}

// validateToken validates the access token by the user info if it's an opaque token
// or by parsing and validating the token if it's a JWT token
func (r *Router) validateAccessToken(c *gin.Context) (string, error) {
	token, err := parseToken(c)
	if err != nil {
		return "", err
	}

	if r.provider.HasSecretKey() {
		return r.provider.VerifyAccessTokenHS256Alg(token)
	}
	return r.provider.VerifyAccessToken(token)
}

// validateTokenWithUserInfo validates the access token by the user info endpoint
func (r *Router) validateTokenWithUserInfo(c *gin.Context) (*idp.ProviderUserInfo, error) {
	accessToken, err := parseToken(c)
	if err != nil {
		return nil, err
	}
	return r.provider.VerifyAccessTokenWithUserInfo(accessToken)
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

func auditApiChanges(c *gin.Context, ctx *pguserauth.Context) {
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
