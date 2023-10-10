package api

import (
	"fmt"
	"net/url"
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
	apiclientkeys "github.com/runopsio/hoop/gateway/api/clientkeys"
	apiconnectionapps "github.com/runopsio/hoop/gateway/api/connectionapps"
	apiplugins "github.com/runopsio/hoop/gateway/api/plugins"
	apiproxymanager "github.com/runopsio/hoop/gateway/api/proxymanager"
	reviewapi "github.com/runopsio/hoop/gateway/api/review"
	sessionapi "github.com/runopsio/hoop/gateway/api/session"
	userapi "github.com/runopsio/hoop/gateway/api/user"
	webhooksapi "github.com/runopsio/hoop/gateway/api/webhooks"
	"github.com/runopsio/hoop/gateway/connection"
	"github.com/runopsio/hoop/gateway/healthz"
	"github.com/runopsio/hoop/gateway/indexer"
	"github.com/runopsio/hoop/gateway/review"
	"github.com/runopsio/hoop/gateway/runbooks"
	"github.com/runopsio/hoop/gateway/security"
	"github.com/runopsio/hoop/gateway/security/idp"
	"github.com/runopsio/hoop/gateway/session"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/user"
	"go.uber.org/zap"
)

type (
	Api struct {
		AgentHandler      agent.Handler
		ConnectionHandler connection.Handler
		UserHandler       user.Handler
		SessionHandler    session.Handler
		IndexerHandler    indexer.Handler
		ReviewHandler     review.Handler
		RunbooksHandler   runbooks.Handler
		SecurityHandler   security.Handler
		IDProvider        *idp.Provider
		GrpcURL           string
		NodeApiURL        *url.URL
		Profile           string
		Analytics         user.Analytics
		logger            *zap.Logger

		StoreV2 *storagev2.Store
	}
)

func (api *Api) StartAPI(sentryInit bool) {
	if os.Getenv("PORT") == "" {
		os.Setenv("PORT", "8009")
	}
	zaplogger := log.NewDefaultLogger()
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
	route.Use(api.proxyNodeAPIMiddleware())
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
	route.GET("/healthz", healthz.LivenessHandler)
	route.GET("/callback", api.SecurityHandler.Callback)

	route.GET("/users",
		api.Authenticate,
		api.TrackRequest(analytics.EventFetchUsers),
		api.AdminOnly,
		api.UserHandler.FindAll)
	route.GET("/users/:id",
		api.Authenticate,
		api.TrackRequest(analytics.EventFetchUsers),
		api.AdminOnly,
		userapi.GetUserByID)
	route.GET("/userinfo",
		api.Authenticate,
		api.TrackRequest(analytics.EventFetchUsers),
		api.UserHandler.Userinfo)
	route.GET("/users/groups",
		api.Authenticate,
		api.UserHandler.UsersGroups)
	route.PUT("/users/:id",
		api.Authenticate,
		api.TrackRequest(analytics.EventUpdateUser),
		api.AdminOnly,
		api.UserHandler.Put)
	route.POST("/users",
		api.Authenticate,
		api.TrackRequest(analytics.EventCreateUser),
		api.AdminOnly,
		userapi.Create)

	route.POST("/connections",
		api.Authenticate,
		api.TrackRequest(analytics.EventCreateConnection),
		api.AdminOnly,
		api.ConnectionHandler.Post)
	route.PUT("/connections/:name",
		api.Authenticate,
		api.TrackRequest(analytics.EventUpdateConnection),
		api.AdminOnly,
		api.ConnectionHandler.Put)
	// DEPRECATED in flavor of POST /sessions
	route.POST("/connections/:name/exec",
		api.Authenticate,
		api.TrackRequest(analytics.EventApiExecConnection),
		api.ConnectionHandler.RunExec)
	route.GET("/connections",
		api.Authenticate,
		api.TrackRequest(analytics.EventFetchConnections),
		api.ConnectionHandler.FindAll)
	route.GET("/connections/:name",
		api.Authenticate,
		api.TrackRequest(analytics.EventFetchConnections),
		api.ConnectionHandler.FindOne)
	route.DELETE("/connections/:name",
		api.Authenticate,
		api.TrackRequest(analytics.EventDeleteConnection),
		api.AdminOnly,
		api.ConnectionHandler.Evict)

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
		api.ReviewHandler.Put)

	route.POST("/agents",
		api.Authenticate,
		api.TrackRequest(analytics.EventCreateAgent),
		api.AdminOnly,
		api.AgentHandler.Post)
	route.GET("/agents",
		api.Authenticate,
		api.AdminOnly,
		api.AgentHandler.FindAll)
	route.DELETE("/agents/:nameOrID",
		api.Authenticate,
		api.TrackRequest(analytics.EventDeleteAgent),
		api.AdminOnly,
		api.AgentHandler.Evict)

	// DEPRECATED in flavor of /api/agents
	route.POST("/clientkeys",
		api.Authenticate,
		api.TrackRequest(analytics.EventCreateClientKey),
		api.AdminOnly,
		apiclientkeys.Post)
	route.GET("/clientkeys",
		api.Authenticate,
		api.TrackRequest(analytics.EventFetchClientKey),
		api.AdminOnly,
		apiclientkeys.List)
	route.GET("/clientkeys/:name",
		api.Authenticate,
		api.TrackRequest(analytics.EventFetchClientKey),
		api.AdminOnly,
		apiclientkeys.Get)
	route.PUT("/clientkeys/:name",
		api.Authenticate,
		api.AdminOnly,
		apiclientkeys.Put)

	route.POST("/plugins",
		api.Authenticate,
		api.TrackRequest(analytics.EventCreatePlugin),
		api.AdminOnly,
		apiplugins.Post)
	route.PUT("/plugins/:name",
		api.Authenticate,
		api.TrackRequest(analytics.EventUdpatePlugin),
		api.AdminOnly,
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
		apiplugins.PutConfig)

	route.GET("/plugins/audit/sessions/:session_id",
		api.Authenticate,
		api.TrackRequest(analytics.EventFetchSessions),
		api.SessionHandler.FindOne)
	// DEPRECATED
	route.GET("/plugins/audit/sessions/:session_id/status",
		api.Authenticate,
		api.SessionHandler.StatusHistory)
	route.GET("/plugins/audit/sessions",
		api.Authenticate,
		api.TrackRequest(analytics.EventFetchSessions),
		api.SessionHandler.FindAll)

	route.GET("/sessions/:session_id",
		api.Authenticate,
		api.TrackRequest(analytics.EventFetchSessions),
		api.SessionHandler.FindOne)
	route.GET("/sessions/:session_id/status",
		api.Authenticate,
		api.SessionHandler.StatusHistory)
	route.GET("/sessions/:session_id/download", api.SessionHandler.DownloadSession)
	route.GET("/sessions",
		api.Authenticate,
		api.TrackRequest(analytics.EventFetchSessions),
		api.SessionHandler.FindAll)
	route.POST("/sessions",
		api.Authenticate,
		api.TrackRequest(analytics.EventApiExecSession),
		sessionapi.Post)
	route.POST("/sessions/:session_id/exec",
		api.Authenticate,
		api.TrackRequest(analytics.EventApiExecReview),
		api.SessionHandler.RunReviewedExec)

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
		webhooksapi.Get)
}

func (api *Api) CreateTrialEntities() error {
	orgId := "test-org"
	userId := "test-user"
	agentId := "test-agent"

	org := user.Org{
		Id:   orgId,
		Name: "hoop",
	}

	u := user.User{
		Id:     userId,
		Org:    orgId,
		Name:   "hooper",
		Email:  "tester@hoop.dev",
		Status: "active",
		Groups: []string{"admin", "sre", "dba", "security", "devops", "support", "engineering"},
	}

	a := agent.Agent{
		Id:    agentId,
		Token: "x-agt-test-token",
		Name:  "test-agent",
		OrgId: orgId,
	}

	_, _ = api.UserHandler.Service.Signup(&org, &u)
	_, err := api.AgentHandler.Service.Persist(&a)
	return err
}
