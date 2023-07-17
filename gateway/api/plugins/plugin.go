package apiplugins

import (
	"net/http"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/gateway/storagev2"
	pluginstorage "github.com/runopsio/hoop/gateway/storagev2/plugin"
	"github.com/runopsio/hoop/gateway/storagev2/types"
)

func Get(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	name := c.Param("name")
	obj, err := pluginstorage.GetByName(ctx, name)
	if err != nil {
		log.Errorf("failed obtaining plugin, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if obj == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "client key not found"})
		return
	}
	redactPluginConfig(obj.Config)
	c.PureJSON(200, obj)
}

func List(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	itemList, err := pluginstorage.List(ctx)
	if err != nil {
		log.Errorf("failed obtaining plugin, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	for _, p := range itemList {
		redactPluginConfig(p.Config)
	}
	c.PureJSON(200, itemList)
}

func redactPluginConfig(c *types.PluginConfig) {
	if c != nil {
		for envKey := range c.EnvVars {
			c.EnvVars[envKey] = "REDACTED"
		}
	}
}
