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
	"github.com/runopsio/hoop/gateway/analytics"
	apiagents "github.com/runopsio/hoop/gateway/api/agents"
	apiconnections "github.com/runopsio/hoop/gateway/api/connections"
	apihealthz "github.com/runopsio/hoop/gateway/api/healthz"
	loginapi "github.com/runopsio/hoop/gateway/api/login"
	apiplugins "github.com/runopsio/hoop/gateway/api/plugins"
	apiproxymanager "github.com/runopsio/hoop/gateway/api/proxymanager"
	reviewapi "github.com/runopsio/hoop/gateway/api/review"
	apiserverinfo "github.com/runopsio/hoop/gateway/api/serverinfo"
	serviceaccountapi "github.com/runopsio/hoop/gateway/api/serviceaccount"
	sessionapi "github.com/runopsio/hoop/gateway/api/session"
	signupapi "github.com/runopsio/hoop/gateway/api/signup"
	userapi "github.com/runopsio/hoop/gateway/api/user"
	webhooksapi "github.com/runopsio/hoop/gateway/api/webhooks"
	"github.com/runopsio/hoop/gateway/indexer"
	"github.com/runopsio/hoop/gateway/review"
	"github.com/runopsio/hoop/gateway/runbooks"
	"github.com/runopsio/hoop/gateway/security/idp"
	"github.com/runopsio/hoop/gateway/storagev2"
	"go.uber.org/zap"
)

type (
	Api struct {
		IndexerHandler  indexer.Handler
		ReviewHandler   review.Handler
		RunbooksHandler runbooks.Handler
		IDProvider      *idp.Provider
		GrpcURL         string
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
	// set standard role to all routes
	route.Use(StandardAccessRole)

	loginHandler := loginapi.New(api.IDProvider)
	route.GET("/login", loginHandler.Login)
	route.GET("/callback", loginHandler.LoginCallback)
	route.GET("/healthz", apihealthz.LivenessHandler())
	route.POST("/signup",
		AnonAccessRole,
		api.Authenticate,
		api.TrackRequest(analytics.EventSignup),
		signupapi.Post)
	route.GET("/users",
		AdminOnlyAccessRole,
		api.Authenticate,
		userapi.List)
	route.GET("/users/:id",
		AdminOnlyAccessRole,
		api.Authenticate,
		userapi.GetUserByID)
	route.GET("/userinfo",
		AnonAccessRole,
		api.Authenticate,
		userapi.GetUserInfo)
	route.PATCH("/users/self/slack",
		api.Authenticate,
		api.TrackRequest(analytics.EventUpdateUser),
		AuditApiChanges,
		userapi.PatchSlackID)
	route.GET("/users/groups",
		api.Authenticate,
		userapi.ListAllGroups)
	route.PUT("/users/:id",
		AdminOnlyAccessRole,
		api.Authenticate,
		api.TrackRequest(analytics.EventUpdateUser),
		AuditApiChanges,
		userapi.Update)
	route.POST("/users",
		AdminOnlyAccessRole,
		api.Authenticate,
		AuditApiChanges,
		userapi.Create)
	route.DELETE("/users/:id",
		AdminOnlyAccessRole,
		api.Authenticate,
		AuditApiChanges,
		userapi.Delete)

	route.GET("/serviceaccounts",
		AdminOnlyAccessRole,
		api.Authenticate,
		serviceaccountapi.List)
	route.POST("/serviceaccounts",
		AdminOnlyAccessRole,
		api.Authenticate,
		api.TrackRequest(analytics.EventCreateServiceAccount),
		AuditApiChanges,
		serviceaccountapi.Create)
	route.PUT("/serviceaccounts/:subject",
		AdminOnlyAccessRole,
		api.Authenticate,
		api.TrackRequest(analytics.EventCreateServiceAccount),
		AuditApiChanges,
		serviceaccountapi.Update)

	route.POST("/connections",
		AdminOnlyAccessRole,
		api.Authenticate,
		api.TrackRequest(analytics.EventCreateConnection),
		AuditApiChanges,
		apiconnections.Post)
	route.PUT("/connections/:nameOrID",
		AdminOnlyAccessRole,
		api.Authenticate,
		api.TrackRequest(analytics.EventUpdateConnection),
		AuditApiChanges,
		apiconnections.Put)
	// DEPRECATED in flavor of POST /sessions
	route.POST("/connections/:name/exec",
		api.Authenticate,
		api.TrackRequest(analytics.EventApiExecConnection),
		apiconnections.RunExec)
	route.GET("/connections",
		api.Authenticate,
		apiconnections.List)
	route.GET("/connections/:nameOrID",
		api.Authenticate,
		apiconnections.Get)
	route.DELETE("/connections/:name",
		AdminOnlyAccessRole,
		api.Authenticate,
		api.TrackRequest(analytics.EventDeleteConnection),
		AuditApiChanges,
		apiconnections.Delete)

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
		AuditApiChanges,
		api.ReviewHandler.Put)

	route.POST("/agents",
		AdminOnlyAccessRole,
		api.Authenticate,
		api.TrackRequest(analytics.EventCreateAgent),
		AuditApiChanges,
		apiagents.Post)
	route.GET("/agents",
		AdminOnlyAccessRole,
		api.Authenticate,
		apiagents.List)
	route.DELETE("/agents/:nameOrID",
		AdminOnlyAccessRole,
		api.Authenticate,
		api.TrackRequest(analytics.EventDeleteAgent),
		AuditApiChanges,
		apiagents.Delete)

	route.POST("/plugins",
		AdminOnlyAccessRole,
		api.Authenticate,
		api.TrackRequest(analytics.EventCreatePlugin),
		AuditApiChanges,
		apiplugins.Post)
	route.PUT("/plugins/:name",
		AdminOnlyAccessRole,
		api.Authenticate,
		api.TrackRequest(analytics.EventUpdatePlugin),
		AuditApiChanges,
		apiplugins.Put)
	route.GET("/plugins",
		api.Authenticate,
		apiplugins.List)
	route.GET("/plugins/:name",
		api.Authenticate,
		apiplugins.Get)
	route.PUT("/plugins/:name/config",
		AdminOnlyAccessRole,
		api.Authenticate,
		api.TrackRequest(analytics.EventUpdatePluginConfig),
		AuditApiChanges,
		apiplugins.PutConfig)

	// alias routes
	route.GET("/plugins/audit/sessions/:session_id",
		api.Authenticate,
		sessionapi.Get)
	route.GET("/plugins/audit/sessions",
		api.Authenticate,
		sessionapi.List)

	route.GET("/sessions/:session_id",
		api.Authenticate,
		sessionapi.Get)
	route.GET("/sessions/:session_id/download", sessionapi.DownloadSession)
	route.GET("/sessions",
		api.Authenticate,
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
		api.RunbooksHandler.ListByConnection,
	)

	route.GET("/plugins/runbooks/templates",
		api.Authenticate,
		api.RunbooksHandler.List,
	)

	route.POST("/plugins/runbooks/connections/:name/exec",
		api.Authenticate,
		api.TrackRequest(analytics.EventExecRunbook),
		api.RunbooksHandler.RunExec)

	route.GET("/webhooks-dashboard",
		AdminOnlyAccessRole,
		api.Authenticate,
		api.TrackRequest(analytics.EventOpenWebhooksDashboard),
		AuditApiChanges,
		webhooksapi.Get)

	route.GET("/serverinfo",
		api.Authenticate,
		apiserverinfo.New(api.GrpcURL).Get)
}
