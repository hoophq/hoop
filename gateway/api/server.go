package api

import (
	"fmt"
	"os"
	"strings"

	"github.com/gin-contrib/static"
	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/gateway/agent"
	"github.com/runopsio/hoop/gateway/connection"
	"github.com/runopsio/hoop/gateway/plugin"
	"github.com/runopsio/hoop/gateway/user"
)

var PROFILE string

type (
	Api struct {
		AgentHandler      agent.Handler
		ConnectionHandler connection.Handler
		UserHandler       user.Handler
		PluginHandler     plugin.Handler
	}
)

func (api *Api) StartAPI() {
	if os.Getenv("PORT") == "" {
		os.Setenv("PORT", "8009")
	}
	route := gin.Default()
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
	rg.Use(api.Authenticate, CORSMiddleware())
	api.buildRoutes(rg)

	if err := route.Run(); err != nil {
		panic("Failed to start HTTP server")
	}
}

func (api *Api) buildRoutes(route *gin.RouterGroup) {
	route.POST("/connections", api.ConnectionHandler.Post)
	route.GET("/connections", api.ConnectionHandler.FindAll)
	route.GET("/connections/:name", api.ConnectionHandler.FindOne)

	route.POST("/agents", api.AgentHandler.Post)
	route.GET("/agents", api.AgentHandler.FindAll)

	route.POST("/plugins", api.PluginHandler.Post)
	route.PUT("/plugins/:name", api.PluginHandler.Put)
	route.GET("/plugins", api.PluginHandler.FindAll)
	route.GET("/plugins/:name", api.PluginHandler.FindOne)

	route.GET("/orgs", api.UserHandler.ListOrgs)
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
		Id:    userId,
		Org:   orgId,
		Name:  "hooper",
		Email: "tester@hoop.dev",
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
