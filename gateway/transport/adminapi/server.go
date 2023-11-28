package adminapi

import (
	"os"
	"time"

	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/common/log"
)

func RunServer(listenAddr string) error {
	zaplogger := log.NewDefaultLogger()
	defer zaplogger.Sync()
	route := gin.New()
	route.Use(ginzap.RecoveryWithZap(zaplogger, false))
	if os.Getenv("GIN_MODE") == "debug" {
		route.Use(ginzap.Ginzap(zaplogger, time.RFC3339, true))
	}
	// https://pkg.go.dev/github.com/gin-gonic/gin#readme-don-t-trust-all-proxies
	route.SetTrustedProxies(nil)
	route.POST("/exec", execPost)
	route.POST("/runbooks/parse", parseRunbookTemplate)
	route.POST("/runbooks/parameters", parseRunbookParameters)
	if listenAddr == "" {
		listenAddr = "127.0.0.1:8099"
	}
	log.Infof("listening admin api at %v", listenAddr)
	return route.Run(listenAddr)
}
