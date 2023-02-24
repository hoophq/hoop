package plugin

import (
	"encoding/base64"
	"fmt"
	"log"
	"net/http"

	"github.com/getsentry/sentry-go"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/user"
)

type (
	Handler struct {
		Service service
	}

	service interface {
		Persist(context *user.Context, plugin *Plugin) error
		PersistConfig(*user.Context, *PluginConfig) error
		FindAll(context *user.Context) ([]ListPlugin, error)
		FindOne(context *user.Context, name string) (*Plugin, error)
	}
)

func redactPluginConfig(c *PluginConfig) {
	if c != nil {
		for envKey, _ := range c.EnvVars {
			c.EnvVars[envKey] = "REDACTED"
		}
	}
}

func (a *Handler) FindOne(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*user.Context)

	name := c.Param("name")
	plugin, err := a.Service.FindOne(context, name)
	if err != nil {
		log.Printf("failed obtaining plugin, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed obtaining plugin"})
		return
	}

	if plugin == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
		return
	}
	redactPluginConfig(plugin.Config)
	c.PureJSON(http.StatusOK, plugin)
}

func (a *Handler) FindAll(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*user.Context)

	plugins, err := a.Service.FindAll(context)
	if err != nil {
		log.Printf("failed listing plugins, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed listing plugins"})
		return
	}
	for _, pl := range plugins {
		redactPluginConfig(pl.Config)
	}
	c.PureJSON(http.StatusOK, plugins)
}

func (a *Handler) Post(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*user.Context)

	var plugin Plugin
	if err := c.ShouldBindJSON(&plugin); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	// it's a ready only field
	plugin.Config = nil

	existingPlugin, err := a.Service.FindOne(context, plugin.Name)
	if err != nil {
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	if existingPlugin != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "Plugin already installed."})
		return
	}

	if err = a.Service.Persist(context, &plugin); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, plugin)
}

// Creates or updates envvars config
func (a *Handler) PutConfig(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*user.Context)

	pluginName := c.Param("name")
	var envVars map[string]string
	if err := c.ShouldBindJSON(&envVars); err != nil {
		log.Printf("failed unmarshalling request, err=%v", err)
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	for env, val := range envVars {
		if len(val) > 100000 { // 0.1MB
			msg := fmt.Sprintf("max size (0.1MB) reached for key %s", env)
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": msg})
			return
		}
		if _, err := base64.StdEncoding.DecodeString(val); err != nil {
			msg := fmt.Sprintf("failed decoding env '%v', err=%v", env, err)
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": msg})
			return
		}
	}
	if len(envVars) == 0 {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "envvars is missing"})
		return
	}
	existingPlugin, err := a.Service.FindOne(context, pluginName)
	if err != nil {
		log.Printf("failed fetching plugin, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed fetching plugin"})
		return
	}
	if existingPlugin == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
		return
	}
	statusCode := http.StatusCreated
	pluginConfigID := uuid.NewString()
	if existingPlugin.Config != nil {
		// update the previous configuration instead of creating another record!
		statusCode = http.StatusOK
		pluginConfigID = existingPlugin.Config.ID
	}

	existingPlugin.ConfigID = &pluginConfigID
	if err := a.Service.Persist(context, existingPlugin); err != nil {
		log.Printf("failed updating existing plugin, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed updating existing plugin"})
		return
	}
	pluginConfigObj := &PluginConfig{
		ID:      pluginConfigID,
		Org:     context.Org.Id,
		EnvVars: envVars,
	}
	err = a.Service.PersistConfig(context, pluginConfigObj)
	if err != nil {
		log.Printf("failed saving plugin config, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed saving plugin config"})
		return
	}
	c.PureJSON(statusCode, pluginConfigObj)
}

func (a *Handler) Put(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*user.Context)

	name := c.Param("name")
	existingPlugin, err := a.Service.FindOne(context, name)
	if err != nil {
		sentry.CaptureException(err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	if existingPlugin == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
		return
	}

	var plugin Plugin
	if err := c.ShouldBindJSON(&plugin); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	// immutable attributes
	plugin.Id = existingPlugin.Id
	plugin.Name = existingPlugin.Name
	plugin.Config = existingPlugin.Config
	if existingPlugin.Config != nil {
		plugin.ConfigID = &existingPlugin.Config.ID
	}

	if plugin.Name == "dlp" {
		for i, conn := range plugin.Connections {
			if len(conn.Config) == 0 {
				plugin.Connections[i].Config = pb.DefaultInfoTypes
			}
		}
	}

	if err = a.Service.Persist(context, &plugin); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	redactPluginConfig(plugin.Config)
	c.PureJSON(http.StatusOK, plugin)
}
