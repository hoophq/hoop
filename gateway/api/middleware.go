package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/common/apiutils"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/version"
	"github.com/runopsio/hoop/gateway/analytics"
	pguserauth "github.com/runopsio/hoop/gateway/pgrest/userauth"
	"github.com/runopsio/hoop/gateway/security/idp"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/user"
	"go.uber.org/zap"
)

var errInvalidAuthHeaderErr = errors.New("invalid authorization header")

func (a *Api) Authenticate(c *gin.Context) {
	roleName := RoleFromContext(c)
	subject, err := a.validateAccessToken(c)
	if err != nil {
		tokenHeader := c.GetHeader("authorization")
		log.Infof("failed authenticating, %v, length=%v, reason=%v",
			parseHeaderForDebug(tokenHeader), len(tokenHeader), err)
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	ctx, err := pguserauth.New().FetchUserContext(subject)
	if err != nil {
		log.Errorf("failed fetching user, subject=%v, err=%v", subject, err)
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	if a.logger != nil && !ctx.IsEmpty() {
		zaplogger := a.logger.With(
			zap.String("org", ctx.OrgName),
			zap.String("user", ctx.UserEmail),
			zap.Bool("isadm", ctx.IsAdmin()),
		)
		c.Set(user.ContextLoggerKey, zaplogger.Sugar())
	}
	switch roleName {
	case RoleStandardAccess: // noop
	case RoleAnonAccess:
		if !ctx.IsEmpty() {
			break
		}
		// Obtain the profile of an anonymous user via the userinfo endpoint
		// this is useful for not having to create a user in the database.
		uinfo, err := a.validateTokenWithUserInfo(c)
		if err != nil {
			log.Warnf("failed authenticating anonymous user via userinfo endpoint, subject=%v, err=%v", subject, err)
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		ctx.UserAnonEmail = uinfo.Email
		ctx.UserAnonProfile = uinfo.Profile
		ctx.UserAnonEmailVerified = uinfo.EmailVerified
		ctx.UserAnonSubject = uinfo.Subject
		ctx.UserAnonPicture = uinfo.Picture
		if ctx.UserAnonEmail == "" || ctx.UserAnonSubject == "" {
			log.Warnf("failed authenticating anonymous user via userinfo endpoint, email=(set=%v), subject=(set=%v)",
				ctx.UserAnonEmail != "", ctx.UserAnonSubject != "")
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Set(storagev2.ContextKey,
			storagev2.NewContext("", "", a.StoreV2).
				WithAnonymousInfo(
					ctx.UserAnonProfile, ctx.UserAnonEmail, ctx.UserAnonSubject,
					ctx.UserAnonPicture, ctx.UserAnonEmailVerified).
				WithApiURL(a.IDProvider.ApiURL).
				WithGrpcURL(a.GrpcURL),
		)
		c.Next()
		return
	case RoleAdminOnly:
		if !ctx.IsAdmin() {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
	default:
		errMsg := fmt.Errorf("failed authenticating, missing role context (%v) for route %v", roleName, c.Request.URL.Path)
		log.Error(errMsg)
		sentry.CaptureException(errMsg)
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	// from this point forward, the user must exists and be verified in the database.
	if ctx.IsEmpty() {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	log.Debugf("user authenticated, role=%s, org=%s, subject=%s, isadmin=%v", roleName, ctx.OrgName, subject, ctx.IsAdmin())

	c.Set(storagev2.ContextKey,
		storagev2.NewContext(ctx.UserSubject, ctx.OrgID, a.StoreV2).
			WithUserInfo(ctx.UserName, ctx.UserEmail, string(ctx.UserStatus), ctx.UserPicture, ctx.UserGroups).
			WithSlackID(ctx.UserSlackID).
			WithOrgName(ctx.OrgName).
			WithApiURL(a.IDProvider.ApiURL).
			WithGrpcURL(a.GrpcURL),
	)
	// TODO: deprecate it in flavor of the context above
	c.Set(user.ContextUserKey, &user.Context{
		Org: &user.Org{Id: ctx.OrgID, Name: ctx.OrgName},
		User: &user.User{
			Id:      ctx.UserSubject,
			Org:     ctx.OrgID,
			Name:    ctx.UserName,
			Email:   ctx.UserEmail,
			Status:  user.StatusType(ctx.UserStatus),
			SlackID: ctx.UserSlackID,
			Groups:  ctx.UserGroups,
		},
	})
	c.Next()
}

func parseToken(c *gin.Context) (string, error) {
	tokenHeader := c.GetHeader("authorization")
	tokenParts := strings.Split(tokenHeader, " ")
	if len(tokenParts) != 2 || tokenParts[0] != "Bearer" || tokenParts[1] == "" {
		return "", errInvalidAuthHeaderErr
	}
	return tokenParts[1], nil
}

// validateAccessToken validates the access token by the user info if it's an opaque token
// or by parsing and validating the token if it's a JWT token
func (a *Api) validateAccessToken(c *gin.Context) (string, error) {
	accessToken, err := parseToken(c)
	if err != nil {
		return "", err
	}
	return a.IDProvider.VerifyAccessToken(accessToken)
}

// validateTokenWithUserInfo validates the access token by the user info endpoint
func (a *Api) validateTokenWithUserInfo(c *gin.Context) (*idp.ProviderUserInfo, error) {
	accessToken, err := parseToken(c)
	if err != nil {
		return nil, err
	}
	return a.IDProvider.VerifyAccessTokenWithUserInfo(accessToken)
}

func AuditApiChanges(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	log.With(
		"subject", ctx.UserID,
		"org", ctx.OrgName,
		"method", c.Request.Method,
		"path", c.Request.URL.Path,
		"user-agent", apiutils.NormalizeUserAgent(c.Request.Header.Values),
		"content-length", c.Request.ContentLength,
	).Info("api-audit")
	c.Next()
}

func (a *Api) TrackRequest(eventName string) func(c *gin.Context) {
	return func(c *gin.Context) {
		ctx := user.ContextUser(c)
		if ctx.User.Email == "" || ctx.Org.Id == "" {
			c.Next()
			return
		}

		properties := map[string]any{
			"host":           c.Request.Host,
			"content-length": c.Request.ContentLength,
			"user-agent":     apiutils.NormalizeUserAgent(c.Request.Header.Values),
		}
		switch eventName {
		case analytics.EventCreateAgent:
			requestBody, _ := io.ReadAll(c.Request.Body)
			data := getBodyAsMap(requestBody)
			reCopyBody(requestBody, c)
			if agentMode, ok := data["mode"]; ok {
				properties["mode"] = fmt.Sprintf("%v", agentMode)
			}
		case analytics.EventUpdateConnection, analytics.EventCreateConnection:
			requestBody, _ := io.ReadAll(c.Request.Body)
			data := getBodyAsMap(requestBody)
			reCopyBody(requestBody, c)
			for key, val := range data {
				switch key {
				case "command":
					properties[key] = ""
					cmd, ok := val.([]any)
					if ok && len(cmd) > 0 {
						properties[key] = fmt.Sprintf("%v", cmd[0])
						continue
					}
					cmd2, ok := val.([]string)
					if ok && len(cmd2) > 0 {
						properties[key] = fmt.Sprintf("%v", cmd2[0])
					}
				case "type", "subtype":
					val := fmt.Sprintf("%v", val)
					// TODO; command must only have the first name of the command
					properties[key] = fmt.Sprintf("%v", val)
				}
			}
		case analytics.EventCreatePlugin, analytics.EventUpdatePlugin, analytics.EventUpdatePluginConfig:
			resourceName, ok := c.Params.Get("name")
			if !ok {
				requestBody, _ := io.ReadAll(c.Request.Body)
				data := getBodyAsMap(requestBody)
				reCopyBody(requestBody, c)
				resourceName = fmt.Sprintf("%v", data["name"])
			}
			if resourceName != "" {
				properties["plugin-name"] = resourceName
			}
		}
		var userEmail string
		if ctx.User != nil {
			userEmail = ctx.User.Email
		}
		analytics.New().Track(userEmail, eventName, properties)
		c.Next()
	}
}

func reCopyBody(requestBody []byte, c *gin.Context) {
	if len(requestBody) == 0 {
		return
	}
	newBody := make([]byte, len(requestBody))
	_ = copy(newBody, requestBody)
	c.Request.Body = io.NopCloser(bytes.NewBuffer(newBody))
}

func getBodyAsMap(data []byte) map[string]any {
	out := map[string]any{}
	_ = json.Unmarshal(data, &out)
	return out
}

func CORSMiddleware() gin.HandlerFunc {
	vs := version.Get()
	return func(c *gin.Context) {
		c.Writer.Header().Set("Server", fmt.Sprintf("hoopgateway/%s", vs.Version))
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, accept, origin, x-backend-api, user-client")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE, PATCH")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
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
