package api

import (
	"fmt"
	"os"
	"strings"
	"time"

	sentrygin "github.com/getsentry/sentry-go/gin"
	"github.com/gin-contrib/static"
	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/gateway/agent"
	"github.com/runopsio/hoop/gateway/analytics"
	apiconnectionapps "github.com/runopsio/hoop/gateway/api/connectionapps"
	apiconnections "github.com/runopsio/hoop/gateway/api/connections"
	apiplugins "github.com/runopsio/hoop/gateway/api/plugins"
	apiproxymanager "github.com/runopsio/hoop/gateway/api/proxymanager"
	reviewapi "github.com/runopsio/hoop/gateway/api/review"
	serviceaccountapi "github.com/runopsio/hoop/gateway/api/serviceaccount"
	sessionapi "github.com/runopsio/hoop/gateway/api/session"
	userapi "github.com/runopsio/hoop/gateway/api/user"
	webhooksapi "github.com/runopsio/hoop/gateway/api/webhooks"
	"github.com/runopsio/hoop/gateway/healthz"
	"github.com/runopsio/hoop/gateway/indexer"
	"github.com/runopsio/hoop/gateway/review"
	"github.com/runopsio/hoop/gateway/runbooks"
	"github.com/runopsio/hoop/gateway/security"
	"github.com/runopsio/hoop/gateway/security/idp"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/user"
	"go.uber.org/zap"
)

type (
	Api struct {
		AgentHandler    agent.Handler
		IndexerHandler  indexer.Handler
		ReviewHandler   review.Handler
		RunbooksHandler runbooks.Handler
		SecurityHandler security.Handler
		IDProvider      *idp.Provider
		GrpcURL         string
		Profile         string
		Analytics       user.Analytics
		logger          *zap.Logger

		StoreV2 *storagev2.Store
	}
)

func (api *Api) StartAPI(sentryInit bool) {
	if os.Getenv("PORT") == "" {
		os.Setenv("PORT", "8009")
	}
	zaplogger := log.NewDefaultLogger(nil)
	defer zaplogger.Sync()
	route := gin.New()
	route.Use(ginzap.RecoveryWithZap(zaplogger, false))
	if os.Getenv("GIN_MODE") == "debug" {
		route.Use(ginzap.Ginzap(zaplogger, time.RFC3339, true))
	}
	api.logger = zaplogger
	// https://pkg.go.dev/github.com/gin-gonic/gin#readme-don-t-trust-all-proxies
	route.SetTrustedProxies(nil)
	route.Use(CORSMiddleware())
	// route.Use(api.proxyNodeAPIMiddleware())
	// UI
	staticUiPath := os.Getenv("STATIC_UI_PATH")
	if staticUiPath == "" {
		staticUiPath = "/app/ui/public"
	}
	route.Use(static.Serve("/", static.LocalFile(staticUiPath, false)))
	route.NoRoute(func(c *gin.Context) {
		if !strings.HasPrefix(c.Request.RequestURI, "/api") {
			c.File(fmt.Sprintf("%s/index.html", staticUiPath))
			return
		}
	})

	rg := route.Group("/api")
	if sentryInit {
		rg.Use(sentrygin.New(sentrygin.Options{
			Repanic: true,
		}))
	}

	api.buildRoutes(rg)
	if err := route.Run(); err != nil {
		log.Fatalf("Failed to start HTTP server, err=%v", err)
	}
}

func (api *Api) buildRoutes(route *gin.RouterGroup) {
	route.GET("/login", api.SecurityHandler.Login)
	route.GET("/callback", api.SecurityHandler.Callback)
	route.GET("/healthz", healthz.LivenessHandler())

	route.GET("/users",
		api.Authenticate,
		api.TrackRequest(analytics.EventFetchUsers),
		api.AdminOnly,
		userapi.List)
	route.GET("/users/:id",
		api.Authenticate,
		api.TrackRequest(analytics.EventFetchUsers),
		api.AdminOnly,
		userapi.GetUserByID)
	route.GET("/userinfo",
		api.Authenticate,
		api.TrackRequest(analytics.EventFetchUsers),
		userapi.GetUserInfo)
	route.PATCH("/users/self/slack",
		api.Authenticate,
		api.TrackRequest(analytics.EventUpdateUser),
		api.AuditApiChanges,
		userapi.PatchSlackID)
	route.GET("/users/groups",
		api.Authenticate,
		userapi.ListAllGroups)
	route.PUT("/users/:id",
		api.Authenticate,
		api.TrackRequest(analytics.EventUpdateUser),
		api.AdminOnly,
		api.AuditApiChanges,
		userapi.Update)
	route.POST("/users",
		api.Authenticate,
		api.AdminOnly,
		api.AuditApiChanges,
		userapi.Create)
	route.DELETE("/users/:id",
		api.Authenticate,
		api.AdminOnly,
		api.AuditApiChanges,
		userapi.Delete)

	route.GET("/serviceaccounts",
		api.Authenticate,
		api.AdminOnly,
		serviceaccountapi.List)
	route.POST("/serviceaccounts",
		api.Authenticate,
		api.TrackRequest(analytics.EventCreateServiceAccount),
		api.AdminOnly,
		api.AuditApiChanges,
		serviceaccountapi.Create)
	route.PUT("/serviceaccounts/:subject",
		api.Authenticate,
		api.TrackRequest(analytics.EventCreateServiceAccount),
		api.AdminOnly,
		api.AuditApiChanges,
		serviceaccountapi.Update)

	route.POST("/connections",
		api.Authenticate,
		api.TrackRequest(analytics.EventCreateConnection),
		api.AdminOnly,
		api.AuditApiChanges,
		apiconnections.Post)
	route.PUT("/connections/:nameOrID",
		api.Authenticate,
		api.TrackRequest(analytics.EventUpdateConnection),
		api.AdminOnly,
		api.AuditApiChanges,
		apiconnections.Put)
	// DEPRECATED in flavor of POST /sessions
	route.POST("/connections/:name/exec",
		api.Authenticate,
		api.TrackRequest(analytics.EventApiExecConnection),
		apiconnections.RunExec)
	route.GET("/connections",
		api.Authenticate,
		api.TrackRequest(analytics.EventFetchConnections),
		apiconnections.List)
	route.GET("/connections/:nameOrID",
		api.Authenticate,
		api.TrackRequest(analytics.EventFetchConnections),
		apiconnections.Get)
	route.DELETE("/connections/:name",
		api.Authenticate,
		api.TrackRequest(analytics.EventDeleteConnection),
		api.AdminOnly,
		api.AuditApiChanges,
		apiconnections.Delete)

	route.POST("/connectionapps",
		api.AuthenticateAgent,
		apiconnectionapps.Post,
	)

	route.POST("/proxymanager/connect",
		api.Authenticate,
		api.TrackRequest(analytics.EventApiProxymanagerConnect),
		apiproxymanager.Post,
	)
	route.POST("/proxymanager/disconnect",
		api.Authenticate,
		apiproxymanager.Disconnect,
	)
	route.GET("/proxymanager/status",
		api.Authenticate,
		apiproxymanager.Get,
	)

	route.GET("/reviews",
		api.Authenticate,
		api.TrackRequest(analytics.EventFetchReviews),
		api.ReviewHandler.FindAll)
	route.GET("/reviews/:id",
		api.Authenticate,
		api.TrackRequest(analytics.EventFetchReviews),
		reviewapi.GetById)
	route.PUT("/reviews/:id",
		api.Authenticate,
		api.TrackRequest(analytics.EventUpdateReview),
		api.AuditApiChanges,
		api.ReviewHandler.Put)

	route.POST("/agents",
		api.Authenticate,
		api.TrackRequest(analytics.EventCreateAgent),
		api.AdminOnly,
		api.AuditApiChanges,
		api.AgentHandler.Post)
	route.GET("/agents",
		api.Authenticate,
		api.AdminOnly,
		api.AgentHandler.FindAll)
	route.DELETE("/agents/:nameOrID",
		api.Authenticate,
		api.TrackRequest(analytics.EventDeleteAgent),
		api.AdminOnly,
		api.AuditApiChanges,
		api.AgentHandler.Evict)

	route.POST("/plugins",
		api.Authenticate,
		api.TrackRequest(analytics.EventCreatePlugin),
		api.AdminOnly,
		api.AuditApiChanges,
		apiplugins.Post)
	route.PUT("/plugins/:name",
		api.Authenticate,
		api.TrackRequest(analytics.EventUdpatePlugin),
		api.AdminOnly,
		api.AuditApiChanges,
		apiplugins.Put)
	route.GET("/plugins",
		api.Authenticate,
		apiplugins.List)
	route.GET("/plugins/:name",
		api.Authenticate,
		apiplugins.Get)
	route.PUT("/plugins/:name/config",
		api.Authenticate,
		api.TrackRequest(analytics.EventUdpatePluginConfig),
		api.AdminOnly,
		api.AuditApiChanges,
		apiplugins.PutConfig)

	// alias routes
	route.GET("/plugins/audit/sessions/:session_id",
		api.Authenticate,
		api.TrackRequest(analytics.EventFetchSessions),
		sessionapi.Get)
	route.GET("/plugins/audit/sessions",
		api.Authenticate,
		api.TrackRequest(analytics.EventFetchSessions),
		sessionapi.List)

	route.GET("/sessions/:session_id",
		api.Authenticate,
		api.TrackRequest(analytics.EventFetchSessions),
		sessionapi.Get)
	route.GET("/sessions/:session_id/download", sessionapi.DownloadSession)
	route.GET("/sessions",
		api.Authenticate,
		api.TrackRequest(analytics.EventFetchSessions),
		sessionapi.List)
	route.POST("/sessions",
		api.Authenticate,
		api.TrackRequest(analytics.EventApiExecSession),
		sessionapi.Post)
	route.POST("/sessions/:session_id/exec",
		api.Authenticate,
		api.TrackRequest(analytics.EventApiExecReview),
		sessionapi.RunReviewedExec)

	route.POST("/plugins/indexer/sessions/search",
		api.Authenticate,
		api.TrackRequest(analytics.EventSearch),
		api.IndexerHandler.Search,
	)

	route.GET("/plugins/runbooks/connections/:name/templates",
		api.Authenticate,
		api.TrackRequest(analytics.EventListRunbooks),
		api.RunbooksHandler.ListByConnection,
	)

	route.GET("/plugins/runbooks/templates",
		api.Authenticate,
		api.TrackRequest(analytics.EventListRunbooks),
		api.RunbooksHandler.List,
	)

	route.POST("/plugins/runbooks/connections/:name/exec",
		api.Authenticate,
		api.TrackRequest(analytics.EventExecRunbook),
		api.RunbooksHandler.RunExec)

	route.GET("/webhooks-dashboard",
		api.Authenticate,
		api.TrackRequest(analytics.EventOpenWebhooksDashboard),
		api.AdminOnly,
		api.AuditApiChanges,
		webhooksapi.Get)
}
