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
	apiproxymanager "github.com/runopsio/hoop/gateway/api/proxymanager"
	reviewapi "github.com/runopsio/hoop/gateway/api/review"
	sessionapi "github.com/runopsio/hoop/gateway/api/session"
	"github.com/runopsio/hoop/gateway/connection"
	"github.com/runopsio/hoop/gateway/healthz"
	"github.com/runopsio/hoop/gateway/indexer"
	"github.com/runopsio/hoop/gateway/plugin"
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
		PluginHandler     plugin.Handler
		SessionHandler    session.Handler
		IndexerHandler    indexer.Handler
		ReviewHandler     review.Handler
		RunbooksHandler   runbooks.Handler
		SecurityHandler   security.Handler
		IDProvider        *idp.Provider
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
		CORSMiddleware()(c)
	})

	rg := route.Group("/api")
	rg.Use(CORSMiddleware())

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
		api.TrackRequest,
		api.AdminOnly,
		api.UserHandler.FindAll)
	route.GET("/users/:id",
		api.Authenticate,
		api.TrackRequest,
		api.AdminOnly,
		api.UserHandler.FindOne)
	route.GET("/userinfo",
		api.Authenticate,
		api.TrackRequest,
		api.UserHandler.Userinfo)
	route.GET("/users/groups",
		api.Authenticate,
		api.TrackRequest,
		api.UserHandler.UsersGroups)
	route.PUT("/users/:id",
		api.Authenticate,
		api.TrackRequest,
		api.AdminOnly,
		api.UserHandler.Put)
	route.POST("/users",
		api.Authenticate,
		api.TrackRequest,
		api.AdminOnly,
		api.UserHandler.Post)

	route.POST("/connections",
		api.Authenticate,
		api.TrackRequest,
		api.AdminOnly,
		api.ConnectionHandler.Post)
	route.PUT("/connections/:name",
		api.Authenticate,
		api.TrackRequest,
		api.AdminOnly,
		api.ConnectionHandler.Put)
	// DEPRECATED in flavor of POST /sessions
	route.POST("/connections/:name/exec",
		api.Authenticate,
		api.TrackRequest,
		api.ConnectionHandler.RunExec)
	route.GET("/connections",
		api.Authenticate,
		api.TrackRequest,
		api.ConnectionHandler.FindAll)
	route.GET("/connections/:name",
		api.Authenticate,
		api.TrackRequest,
		api.ConnectionHandler.FindOne)
	route.DELETE("/connections/:name",
		api.Authenticate,
		api.TrackRequest,
		api.AdminOnly,
		api.ConnectionHandler.Evict)

	route.POST("/proxymanager/connect",
		api.Authenticate,
		api.TrackRequest,
		apiproxymanager.Post,
	)
	route.POST("/proxymanager/disconnect",
		api.Authenticate,
		api.TrackRequest,
		apiproxymanager.Disconnect,
	)
	route.GET("/proxymanager/status",
		api.Authenticate,
		api.TrackRequest,
		apiproxymanager.Get,
	)

	route.GET("/reviews",
		api.Authenticate,
		api.TrackRequest,
		api.ReviewHandler.FindAll)
	route.GET("/reviews/:id",
		api.Authenticate,
		api.TrackRequest,
		reviewapi.GetById)
	route.PUT("/reviews/:id",
		api.Authenticate,
		api.TrackRequest,
		api.ReviewHandler.Put)

	route.POST("/agents",
		api.Authenticate,
		api.TrackRequest,
		api.AdminOnly,
		api.AgentHandler.Post)
	route.GET("/agents",
		api.Authenticate,
		api.TrackRequest,
		api.AdminOnly,
		api.AgentHandler.FindAll)
	route.DELETE("/agents/:nameOrID",
		api.Authenticate,
		api.TrackRequest,
		api.AdminOnly,
		api.AgentHandler.Evict)

	route.POST("/plugins",
		api.Authenticate,
		api.TrackRequest,
		api.AdminOnly,
		api.PluginHandler.Post)
	route.PUT("/plugins/:name",
		api.Authenticate,
		api.TrackRequest,
		api.AdminOnly,
		api.PluginHandler.Put)
	route.GET("/plugins",
		api.Authenticate,
		api.TrackRequest,
		api.PluginHandler.FindAll)
	route.GET("/plugins/:name",
		api.Authenticate,
		api.TrackRequest,
		api.PluginHandler.FindOne)

	route.PUT("/plugins/:name/config",
		api.Authenticate,
		api.TrackRequest,
		api.AdminOnly,
		api.PluginHandler.PutConfig)

	route.GET("/plugins/audit/sessions/:session_id",
		api.Authenticate,
		api.TrackRequest,
		api.SessionHandler.FindOne)
	route.GET("/plugins/audit/sessions/:session_id/status",
		api.Authenticate,
		api.TrackRequest,
		api.SessionHandler.StatusHistory)
	route.GET("/plugins/audit/sessions",
		api.Authenticate,
		api.TrackRequest,
		api.SessionHandler.FindAll)

	route.GET("/sessions/:session_id",
		api.Authenticate,
		api.TrackRequest,
		api.SessionHandler.FindOne)
	route.GET("/sessions/:session_id/status",
		api.Authenticate,
		api.TrackRequest,
		api.SessionHandler.StatusHistory)
	route.GET("/sessions",
		api.Authenticate,
		api.TrackRequest,
		api.SessionHandler.FindAll)
	route.POST("/sessions",
		api.Authenticate,
		api.TrackRequest,
		sessionapi.Post)
	route.POST("/sessions/:session_id/exec",
		api.Authenticate,
		api.TrackRequest,
		api.SessionHandler.RunReviewedExec)

	route.POST("/plugins/indexer/sessions/search",
		api.Authenticate,
		api.TrackRequest,
		api.IndexerHandler.Search,
	)

	route.GET("/plugins/runbooks/connections/:name/templates",
		api.Authenticate,
		api.TrackRequest,
		api.RunbooksHandler.ListByConnection,
	)

	route.GET("/plugins/runbooks/templates",
		api.Authenticate,
		api.TrackRequest,
		api.RunbooksHandler.List,
	)

	route.POST("/plugins/runbooks/connections/:name/exec",
		api.Authenticate,
		api.TrackRequest,
		api.RunbooksHandler.RunExec)
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
		Id:          agentId,
		Token:       "x-agt-test-token",
		Name:        "test-agent",
		OrgId:       orgId,
		CreatedById: userId,
	}

	_, _ = api.UserHandler.Service.Signup(&org, &u)
	_, err := api.AgentHandler.Service.Persist(&a)
	return err
}
