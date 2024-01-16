package api

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/common/dsnkeys"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/version"
	pguserauth "github.com/runopsio/hoop/gateway/pgrest/userauth"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/user"
	"go.uber.org/zap"
)

var errInvalidAuthHeaderErr = errors.New("invalid authorization header")

func (api *Api) Authenticate(c *gin.Context) {
	sub, err := api.validateClaims(c)
	if err != nil {
		tokenHeader := c.GetHeader("authorization")
		log.Infof("failed authenticating, %v, length=%v, reason=%v",
			parseHeaderForDebug(tokenHeader), len(tokenHeader), err)
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	ctx, err := pguserauth.New().FetchUserContext(sub)
	if err != nil || ctx.IsEmpty() {
		log.Debugf("failed searching for user, subject=%v, err=%v", sub, err)
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	if api.logger != nil {
		zaplogger := api.logger.With(
			zap.String("org", ctx.OrgName),
			zap.String("user", ctx.UserEmail),
			zap.Bool("isadm", ctx.IsAdmin()),
		)
		c.Set(user.ContextLoggerKey, zaplogger.Sugar())
	}

	c.Set(storagev2.ContextKey,
		storagev2.NewContext(ctx.UserSubject, ctx.OrgID, api.StoreV2).
			WithUserInfo(ctx.UserName, ctx.UserEmail, string(ctx.UserStatus), ctx.UserGroups).
			WithOrgName(ctx.OrgName).
			WithApiURL(api.IDProvider.ApiURL).
			WithGrpcURL(api.GrpcURL),
	)
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

func (api *Api) AuditApiChanges(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	log.With(
		"subject", ctx.UserID,
		"org", ctx.OrgName,
		"method", c.Request.Method,
		"path", c.Request.URL.Path,
		"user-agent", c.GetHeader("user-agent"),
		"content-length", c.Request.ContentLength,
	).Info("api-audit")
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
