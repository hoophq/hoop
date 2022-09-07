package main

import (
	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/gateway/api"
)

func startAPI() {
	a, err := api.NewAPI()
	if err != nil {
		panic("Failed lo load storage module")
	}

	route := gin.Default()

	route.Use(a.Authenticate)

	buildRoutes(route, a)

	if err = route.Run(); err != nil {
		panic("Failed to start HTTP server")
	}
}

func buildRoutes(route *gin.Engine, api *api.Api) {
	route.GET("/connections", api.GetConnections)
	route.GET("/connections/:name", api.GetConnection)
	route.POST("/connections", api.PostConnection)
}
