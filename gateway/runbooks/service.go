package runbooks

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/gateway/connection"
	"github.com/runopsio/hoop/gateway/plugin"
	"github.com/runopsio/hoop/gateway/user"
)

type pluginService interface {
	FindOne(context *user.Context, name string) (*plugin.Plugin, error)
}

type connectionService interface {
	FindOne(context *user.Context, name string) (*connection.Connection, error)
}

func getAccessToken(c *gin.Context) string {
	tokenHeader := c.GetHeader("authorization")
	tokenParts := strings.Split(tokenHeader, " ")
	if len(tokenParts) > 1 {
		return tokenParts[1]
	}
	return ""
}
