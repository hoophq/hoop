package main

import (
	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/gateway/api"
)

func CORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, accept, origin")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

func startAPI() {
	a, err := api.NewAPI()
	if err != nil {
		panic("Failed lo load storage module")
	}

	route := gin.Default()

	route.Use(a.Authenticate, CORSMiddleware())

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
