package pgusers

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const (
	ContextLoggerKey = "context-logger"
	LicenseFreeType  = "free"
)

var ErrOrgAlreadyExists = fmt.Errorf("organization already exists")

func ContextLogger(c *gin.Context) *zap.SugaredLogger {
	obj, _ := c.Get(ContextLoggerKey)
	if logger := obj.(*zap.SugaredLogger); logger != nil {
		return logger
	}
	return zap.NewNop().Sugar()
}
