package main

import (
	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/gateway/api"
)

func serveHTTP() {
	a, err := api.NewAPI()
	if err != nil {
		panic("Failed to create API spec")
	}

	route := gin.Default()
	buildRoutes(route, a)

	if err = route.Run(); err != nil {
		panic("Failed to start HTTP server")
	}
}

func buildRoutes(route *gin.Engine, api *api.Api) {
	route.GET("/secrets", api.GetSecrets)
	route.POST("/secrets", api.PostSecrets)

	route.GET("/connections", api.GetConnections)
	route.POST("/connections", api.PostConnection)
}
