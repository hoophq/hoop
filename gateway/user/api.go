package user

import (
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"go.uber.org/zap"

	"github.com/gin-gonic/gin"
)

type (
	Handler struct{}

	Analytics interface {
		Identify(ctx *types.APIContext)
		Track(ctx *types.APIContext, eventName string, properties map[string]any)
	}
)

// ContextLogger do a best effort to get the context logger,
// if it fail to retrieve, returns a noop logger
func ContextLogger(c *gin.Context) *zap.SugaredLogger {
	obj, _ := c.Get(ContextLoggerKey)
	if logger := obj.(*zap.SugaredLogger); logger != nil {
		return logger
	}
	return zap.NewNop().Sugar()
}

// ContextUser do a best effort to get the user context from the request
// if it fail, it will return an empty one that can be used safely
func ContextUser(c *gin.Context) *Context {
	obj, _ := c.Get(ContextUserKey)
	ctx, _ := obj.(*Context)
	if ctx == nil {
		return &Context{Org: &Org{}, User: &User{}}
	}
	if ctx.Org == nil {
		ctx.Org = &Org{}
	}
	if ctx.User == nil {
		ctx.User = &User{}
	}
	return ctx
}
