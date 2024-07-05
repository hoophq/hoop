package pgusers

import (
	"os"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const (
	ContextLoggerKey = "context-logger"
	LicenseFreeType  = "free"
)

func IsOrgMultiTenant() bool { return os.Getenv("ORG_MULTI_TENANT") == "true" }

func ContextLogger(c *gin.Context) *zap.SugaredLogger {
	obj, _ := c.Get(ContextLoggerKey)
	if logger := obj.(*zap.SugaredLogger); logger != nil {
		return logger
	}
	return zap.NewNop().Sugar()
}
