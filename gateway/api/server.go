package api

import (
	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/gateway/agent"
	"github.com/runopsio/hoop/gateway/connection"
	"github.com/runopsio/hoop/gateway/user"
)

type (
	Api struct {
		AgentHandler      agent.Handler
		ConnectionHandler connection.Handler
		UserHandler       user.Handler
	}
)

func (api *Api) CreateTrialEntities() error {
	orgId := "test-org"
	userId := "test-user"

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
		Token:       "x-agt-test-token",
		Name:        "test-agent",
		OrgId:       orgId,
		CreatedById: userId,
	}

	_, err := api.UserHandler.Service.Signup(&org, &u)
	_, err = api.AgentHandler.Service.Persist(&a)

	if err != nil {
		return err
	}

	return nil
}

func (api *Api) StartAPI() {
	route := gin.Default()
	route.Use(api.Authenticate, CORSMiddleware())

	api.buildRoutes(route)

	if err := route.Run(); err != nil {
		panic("Failed to start HTTP server")
	}
}

func (api *Api) buildRoutes(route *gin.Engine) {
	route.PUT("/connections/:id", api.ConnectionHandler.Update)
	route.POST("/connections", api.ConnectionHandler.Post)
	route.GET("/connections", api.ConnectionHandler.FindAll)
	route.GET("/connections/:name", api.ConnectionHandler.FindOne)

	route.POST("/agents", api.AgentHandler.Post)
	route.GET("/agents", api.AgentHandler.FindAll)
}
