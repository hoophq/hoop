package api

import (
	"fmt"
	"os"
	"strings"

	"github.com/runopsio/hoop/gateway/review"

	"github.com/runopsio/hoop/gateway/security"
	"github.com/runopsio/hoop/gateway/security/idp"

	"github.com/gin-contrib/static"
	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/gateway/agent"
	"github.com/runopsio/hoop/gateway/connection"
	"github.com/runopsio/hoop/gateway/plugin"
	"github.com/runopsio/hoop/gateway/session"
	"github.com/runopsio/hoop/gateway/user"
)

type (
	Api struct {
		AgentHandler      agent.Handler
		ConnectionHandler connection.Handler
		UserHandler       user.Handler
		PluginHandler     plugin.Handler
		SessionHandler    session.Handler
		ReviewHandler     review.Handler
		SecurityHandler   security.Handler
		IDProvider        *idp.Provider
		Profile           string
	}
)

func (api *Api) StartAPI() {
	if os.Getenv("PORT") == "" {
		os.Setenv("PORT", "8009")
	}
	route := gin.Default()
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
	api.buildRoutes(rg)

	if err := route.Run(); err != nil {
		panic("Failed to start HTTP server")
	}
}

func (api *Api) buildRoutes(route *gin.RouterGroup) {
	route.GET("/login", api.SecurityHandler.Login)
	route.GET("/callback", api.SecurityHandler.Callback)

	route.GET("/users", api.Authenticate, api.UserHandler.FindAll)
	route.GET("/users/:id", api.Authenticate, api.UserHandler.FindOne)
	route.PUT("/users/:id", api.Authenticate, api.UserHandler.Put)
	route.POST("/users", api.Authenticate, api.UserHandler.Post)

	route.POST("/connections", api.Authenticate, api.ConnectionHandler.Post)
	route.GET("/connections", api.Authenticate, api.ConnectionHandler.FindAll)
	route.GET("/connections/:name", api.Authenticate, api.ConnectionHandler.FindOne)
	route.PUT("/connections/:name", api.Authenticate, api.ConnectionHandler.Put)

	route.GET("/reviews", api.Authenticate, api.ReviewHandler.FindAll)
	route.GET("/reviews/:id", api.Authenticate, api.ReviewHandler.FindOne)
	route.PUT("/reviews/:id", api.Authenticate, api.ReviewHandler.Put)

	route.POST("/agents", api.Authenticate, api.AgentHandler.Post)
	route.GET("/agents", api.Authenticate, api.AgentHandler.FindAll)

	route.POST("/plugins", api.Authenticate, api.PluginHandler.Post)
	route.PUT("/plugins/:name", api.Authenticate, api.PluginHandler.Put)
	route.GET("/plugins", api.Authenticate, api.PluginHandler.FindAll)
	route.GET("/plugins/:name", api.Authenticate, api.PluginHandler.FindOne)

	route.GET("/plugins/audit/sessions/:session_id", api.Authenticate, api.SessionHandler.FindOne)
	route.GET("/plugins/audit/sessions", api.Authenticate, api.SessionHandler.FindAll)
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
		Groups: []string{"admin"},
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
