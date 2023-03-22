package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/runopsio/hoop/common/log"

	"github.com/gin-gonic/gin"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/common/version"
	"github.com/runopsio/hoop/gateway/user"
	"go.uber.org/zap"
)

var invalidAuthErr = errors.New("invalid auth")

func (api *Api) Authenticate(c *gin.Context) {
	sub, err := api.validateClaims(c)
	if err != nil {
		log.Printf("failed authenticating, err=%v", err)
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	ctx, err := api.UserHandler.Service.FindBySub(sub)
	if err != nil || ctx.User == nil {
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
	c.Set(user.ContextUserKey, ctx)
	c.Next()
}

func (api *Api) validateClaims(c *gin.Context) (string, error) {
	if api.Profile == pb.DevProfile {
		return "test-user", nil
	}

	tokenHeader := c.GetHeader("authorization")
	tokenParts := strings.Split(tokenHeader, " ")
	if len(tokenParts) != 2 || tokenParts[0] != "Bearer" || tokenParts[1] == "" {
		return "", invalidAuthErr
	}
	return api.IDProvider.VerifyAccessToken(tokenParts[1])
}

func (api *Api) AdminOnly(c *gin.Context) {
	context := user.ContextUser(c)

	if !context.User.IsAdmin() {
		c.AbortWithStatus(401)
		return
	}

	c.Next()
}

func (api *Api) TrackRequest(c *gin.Context) {
	context := user.ContextUser(c)

	api.Analytics.Track(context.User.Id, fmt.Sprintf("%s %s", c.Request.Method, c.Request.RequestURI), map[string]any{
		"host":           c.Request.Host,
		"content-length": c.Request.ContentLength,
		"user-agent":     c.Request.Header.Get("User-Agent"),
	})

	c.Next()
}

func CORSMiddleware() gin.HandlerFunc {
	vs := version.Get()
	return func(c *gin.Context) {
		c.Writer.Header().Set("Server", fmt.Sprintf("hoopgateway/%s", vs.Version))
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, accept, origin")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}
