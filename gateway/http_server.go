package main

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/gateway/api"
)

const MemDb = "../plugins/storage/mem_db.so"

func startAPI(storagePlugin string) {
	a, err := api.NewAPI(storagePlugin)
	if err != nil {
		fmt.Printf("No storage plugin found. Starting with in-memory persistence")
		a, err = api.NewAPI(MemDb)
		if err != nil {
			panic("Failed lo load storage module")
		}
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
