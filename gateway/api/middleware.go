package api

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/common/version"
	"github.com/runopsio/hoop/gateway/apiclient"
	apitypes "github.com/runopsio/hoop/gateway/apiclient/types"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/user"
	"go.uber.org/zap"
)

var (
	errInvalidAuthHeaderErr = errors.New("invalid authorization header")
	debugRoutes             = os.Getenv("DEBUG_ROUTES") == "1" || os.Getenv("DEBUG_ROUTES") == "true"
)

func (api *Api) proxyNodeAPIMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if debugRoutes {
			log.Infof(`%s %s %v %s`,
				c.Request.Method,
				c.Request.URL.Path,
				c.Request.ContentLength,
				c.Request.Header.Get("user-agent"),
			)
		}
		if !strings.HasPrefix(c.Request.URL.Path, "/api/") ||
			// connectionsapp authenticate agents, skip it
			c.Request.URL.Path == "/api/connectionapps" {
			c.Next()
			return
		}

		sub, err := api.validateClaims(c)
		switch err {
		case errInvalidAuthHeaderErr:
			// It's not an authenticated route, or the client didn't pass a valid header.
			// Let the next middleware to decide what to do.
			c.Next()
			return
		case nil: // noop
		default:
			// It found a bearer token and for some reason failed to validate.
			// End the request in this middleware
			tokenHeader := c.GetHeader("authorization")
			log.Infof("failed authenticating (proxy layer), %v, length=%v, reason=%v",
				parseHeaderForDebug(tokenHeader), len(tokenHeader), err)
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		// once we have the subject, we must enforce the authentication in this layer
		ctx, err := api.UserHandler.Service.FindBySub(sub)
		if err != nil || ctx.User == nil {
			log.Debugf("failed searching for user, sub=%v, err=%v", sub, err)
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		backendAPI := c.Request.Header.Get("x-backend-api")
		if backendAPI == "express" || ctx.Org.IsApiV2 {
			director := func(req *http.Request) {
				req.Header = c.Request.Header
				req.URL.Scheme = "http"
				req.URL.Host = api.NodeApiURL.Host
				req.URL.Path = c.Request.URL.Path
			}
			proxy := &httputil.ReverseProxy{Director: director}
			proxy.ServeHTTP(c.Writer, c.Request)
			c.Abort()
			return
		}

		// The request was not proxied.
		// Set the user context for the next layer
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
}

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

	ctx, err := api.UserHandler.Service.FindBySub(sub)
	if err != nil || ctx.User == nil {
		log.Debugf("failed searching for user, sub=%v, err=%v", sub, err)
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

	client := apiclient.New(tokenParts[1])
	if u, _ := url.Parse(tokenParts[1]); u != nil && len(u.Path) == 65 {
		// it is an old dsn, maintain compatibility
		// <scheme>://<host>:<port>/<secretkey-hash>
		ag, err := client.AuthClientKeys()
		if err != nil {
			if err != apiclient.ErrUnauthorized {
				log.Warnf("failed authenticating agent (clientkey), %v, length=%v, err=%v",
					parseHeaderForDebug(tokenHeader), len(tokenHeader), err)
				sentry.CaptureException(fmt.Errorf("failed authentication agent dsn, err=%v", err))
			}
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		c.Set(storagev2.ContextKey,
			storagev2.NewDSNContext(ag.ID, ag.OrgID, ag.Name, api.StoreV2).
				WithApiURL(api.IDProvider.ApiURL).
				WithGrpcURL(api.GrpcURL))
		c.Next()
		return
	}
	// fallback to agent dsn keys
	ag, err := client.AuthAgent(apitypes.AgentAuthRequest{Status: "DISCONNECTED"})
	if err != nil {
		if err != apiclient.ErrUnauthorized {
			log.Warnf("failed authenticating agent (agent dsn), %v, length=%v, err=%v",
				parseHeaderForDebug(tokenHeader), len(tokenHeader), err)
			sentry.CaptureException(fmt.Errorf("failed authentication agent dsn, err=%v", err))
		}
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	c.Set(storagev2.ContextKey,
		storagev2.NewDSNContext(ag.ID, ag.OrgID, ag.Name, api.StoreV2).
			WithApiURL(api.IDProvider.ApiURL).
			WithGrpcURL(api.GrpcURL))
	c.Next()
}

func (api *Api) validateClaims(c *gin.Context) (string, error) {
	if api.Profile == pb.DevProfile {
		return "test-user", nil
	}

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
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE")

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
