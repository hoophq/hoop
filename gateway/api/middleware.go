package api

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/common/dsnkeys"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/version"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/user"
	"go.uber.org/zap"
)

var (
	errInvalidAuthHeaderErr = errors.New("invalid authorization header")
	debugRoutes             = os.Getenv("DEBUG_ROUTES") == "1" || os.Getenv("DEBUG_ROUTES") == "true"
)

func (api *Api) Authenticate(c *gin.Context) {
	// validate if the proxy layer performed the authentication
	// in this case just set the logger and do nothing.
	if obj, exists := c.Get(user.ContextUserKey); exists {
		if ctx, _ := obj.(*user.Context); ctx != nil {
			if api.logger != nil {
				zaplogger := api.logger.With(
					zap.String("org", ctx.User.Org),
					zap.String("user", ctx.User.Email),
					zap.Bool("isadm", ctx.User.IsAdmin()),
				)
				c.Set(user.ContextLoggerKey, zaplogger.Sugar())
			}
		}
		c.Next()
		return
	}
	// perform the normal authentication, the proxy was unable to
	// to authenticate the request.
	sub, err := api.validateClaims(c)
	if err != nil {
		tokenHeader := c.GetHeader("authorization")
		log.Infof("failed authenticating, %v, length=%v, reason=%v",
			parseHeaderForDebug(tokenHeader), len(tokenHeader), err)
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	ctx, err := user.GetUserContext(api.UserHandler.Service, sub)
	if err != nil || ctx.User == nil {
		log.Debugf("failed searching for user, subject=%v, err=%v", sub, err)
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	if api.logger != nil {
		zaplogger := api.logger.With(
			zap.String("org", ctx.User.Org),
			zap.String("user", ctx.User.Email),
			zap.Bool("isadm", ctx.User.IsAdmin()),
		)
		c.Set(user.ContextLoggerKey, zaplogger.Sugar())
	}

	c.Set(storagev2.ContextKey,
		storagev2.NewContext(ctx.User.Id, ctx.Org.Id, api.StoreV2).
			WithUserInfo(ctx.User.Name, ctx.User.Email, string(ctx.User.Status), ctx.User.Groups).
			WithOrgName(ctx.Org.Name).
			WithApiURL(api.IDProvider.ApiURL).
			WithGrpcURL(api.GrpcURL),
	)
	c.Set(user.ContextUserKey, ctx)
	c.Next()
}

// TODO: refactor to perform unary calls instead of relying in the public api
func (api *Api) AuthenticateAgent(c *gin.Context) {
	tokenHeader := c.GetHeader("authorization")
	tokenParts := strings.Split(tokenHeader, " ")
	if len(tokenParts) != 2 || tokenParts[0] != "Bearer" || tokenParts[1] == "" {
		log.Debugf("failed authenticating agent, %v, length=%v",
			parseHeaderForDebug(tokenHeader), len(tokenHeader))
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	// fallback to agent dsn keys
	dsn, err := dsnkeys.Parse(tokenParts[1])
	if err != nil {
		log.Debugf("failed parsing dsn (agent dsn), %v, length=%v, err=%v",
			parseHeaderForDebug(tokenHeader), len(tokenHeader), err)
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	ag, err := api.AgentHandler.Service.FindByToken(dsn.SecretKeyHash)
	if ag == nil || err != nil {
		log.Debugf("failed authenticating agent (agent dsn), %v, length=%v, err=%v",
			parseHeaderForDebug(tokenHeader), len(tokenHeader), err)
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	if ag.Name != dsn.Name || ag.Mode != dsn.AgentMode {
		log.Errorf("failed authenticating agent (agent dsn), mismatch dsn attributes. id=%v, name=%v, mode=%v",
			ag.Id, dsn.Name, dsn.AgentMode)
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	c.Set(storagev2.ContextKey,
		storagev2.NewDSNContext(ag.Id, ag.OrgId, ag.Name, api.StoreV2).
			WithApiURL(api.IDProvider.ApiURL).
			WithGrpcURL(api.GrpcURL))
	c.Next()
}

func (api *Api) validateClaims(c *gin.Context) (string, error) {
	tokenHeader := c.GetHeader("authorization")
	tokenParts := strings.Split(tokenHeader, " ")
	if len(tokenParts) != 2 || tokenParts[0] != "Bearer" || tokenParts[1] == "" {
		return "", errInvalidAuthHeaderErr
	}
	return api.IDProvider.VerifyAccessToken(tokenParts[1])
}

func (api *Api) AdminOnly(c *gin.Context) {
	context := user.ContextUser(c)

	if !context.User.IsAdmin() {
		c.AbortWithStatus(403)
		return
	}

	c.Next()
}

func (api *Api) TrackRequest(eventName string) func(c *gin.Context) {
	return func(c *gin.Context) {
		context := user.ContextUser(c)
		api.Analytics.Track(context.ToAPIContext(), eventName, map[string]any{
			"host":           c.Request.Host,
			"content-length": c.Request.ContentLength,
			"user-agent":     c.Request.Header.Get("User-Agent"),
		})
		c.Next()
	}
}

func CORSMiddleware() gin.HandlerFunc {
	vs := version.Get()
	return func(c *gin.Context) {
		c.Writer.Header().Set("Server", fmt.Sprintf("hoopgateway/%s", vs.Version))
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, accept, origin, x-backend-api")
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
