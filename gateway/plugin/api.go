package plugin

import (
	"encoding/base64"
	"fmt"
	"net/http"

	"github.com/runopsio/hoop/common/log"

	"github.com/getsentry/sentry-go"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/storagev2"
	pluginstorage "github.com/runopsio/hoop/gateway/storagev2/plugin"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
	"github.com/runopsio/hoop/gateway/user"
)

type (
	Handler struct {
		Service           service
		RegisteredPlugins []plugintypes.Plugin
	}

	service interface {
		Persist(context *user.Context, plugin *Plugin) error
		PersistConfig(*user.Context, *PluginConfig) error
		FindAll(context *user.Context) ([]types.Plugin, error)
		FindOne(context *user.Context, name string) (*types.Plugin, error)
	}
)

func redactPluginConfig(c *PluginConfig) {
	if c != nil {
		for envKey := range c.EnvVars {
			c.EnvVars[envKey] = "REDACTED"
		}
	}
}

func (a *Handler) Post(c *gin.Context) {
	context := user.ContextUser(c)
	ctxv2 := storagev2.ParseContext(c)
	var plugin Plugin
	if err := c.ShouldBindJSON(&plugin); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	existingPlugin, err := pluginstorage.GetByName(ctxv2, plugin.Name)
	if err != nil {
		log.Errorf("failed retrieving existing plugin, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	if existingPlugin != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "Plugin already installed."})
		return
	}

	if err := processOnUpdatePluginPhase(a.RegisteredPlugins, nil, parseToV2(&plugin)); err != nil {
		msg := fmt.Sprintf("failed initializing plugin, reason=%v", err)
		log.Errorf(msg)
		sentry.CaptureException(err)
		c.JSON(http.StatusBadRequest, gin.H{"message": msg})
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
	context := user.ContextUser(c)
	ctxv2 := storagev2.ParseContext(c)
	log := user.ContextLogger(c)

	pluginName := c.Param("name")
	var envVars map[string]string
	if err := c.ShouldBindJSON(&envVars); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if err := validatePluginConfig(&PluginConfig{EnvVars: envVars}); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}
	existingPlugin, err := pluginstorage.GetByName(ctxv2, pluginName)
	if err != nil {
		log.Errorf("failed fetching plugin, err=%v", err)
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
	var connectionList []Connection
	for _, conn := range existingPlugin.Connections {
		connectionList = append(connectionList, Connection{
			Id:           conn.ID,
			ConnectionId: conn.ConnectionID,
			Name:         conn.Name,
			Config:       conn.Config,
			Groups:       nil, // Groups isn't used
		})
	}
	// only envvars is changed in this method
	newState := parseToV2(&Plugin{
		OrgId:       existingPlugin.OrgID,
		Name:        existingPlugin.Name,
		Connections: connectionList,
		Config:      &PluginConfig{EnvVars: envVars},
	})
	if err := processOnUpdatePluginPhase(a.RegisteredPlugins, existingPlugin, newState); err != nil {
		msg := fmt.Sprintf("failed initializing plugin, reason=%v", err)
		log.Errorf(msg)
		sentry.CaptureException(err)
		c.JSON(http.StatusBadRequest, gin.H{"message": msg})
		return
	}
	if err := a.Service.Persist(context, ParseToV1(existingPlugin)); err != nil {
		log.Errorf("failed updating existing plugin, err=%v", err)
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
		log.Errorf("failed saving plugin config, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed saving plugin config"})
		return
	}
	c.PureJSON(statusCode, pluginConfigObj)
}

func (a *Handler) Put(c *gin.Context) {
	context := user.ContextUser(c)
	ctxv2 := storagev2.ParseContext(c)
	log := user.ContextLogger(c)
	name := c.Param("name")
	existingPlugin, err := pluginstorage.GetByName(ctxv2, name)
	if err != nil {
		log.Errorf("failed fetching plugin, err=%v", err)
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
	plugin.OrgId = existingPlugin.OrgID
	plugin.Id = existingPlugin.ID
	plugin.Name = existingPlugin.Name
	plugin.Config = &PluginConfig{
		ID:      existingPlugin.ID,
		Org:     existingPlugin.OrgID,
		EnvVars: existingPlugin.Config.EnvVars,
	}
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

	if err := processOnUpdatePluginPhase(a.RegisteredPlugins, existingPlugin, parseToV2(&plugin)); err != nil {
		msg := fmt.Sprintf("failed initializing plugin, reason=%v", err)
		log.Errorf(msg)
		sentry.CaptureException(err)
		c.JSON(http.StatusBadRequest, gin.H{"message": msg})
		return
	}
	if err = a.Service.Persist(context, &plugin); err != nil {
		log.Errorf("failed saving plugin, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	redactPluginConfig(plugin.Config)
	c.PureJSON(http.StatusOK, plugin)
}

func validatePluginConfig(config *PluginConfig) error {
	if len(config.EnvVars) == 0 {
		return nil
	}
	for key, val := range config.EnvVars {
		if key == "" {
			return fmt.Errorf("missing config key")
		}
		if val == "" {
			return fmt.Errorf("missing config val for key=%s", key)
		}
		if len(val) > 100000 { // 0.1MB
			return fmt.Errorf("max size (0.1MB) reached for key %s", key)
		}
		if _, err := base64.StdEncoding.DecodeString(val); err != nil {
			return fmt.Errorf("failed decoding key '%v', err=%v", key, err)
		}
	}
	return nil
}

func ParseToV1(p *types.Plugin) *Plugin {
	p2 := &Plugin{
		Id:             p.ID,
		OrgId:          p.OrgID,
		ConfigID:       p.ConfigID,
		Config:         nil,
		Source:         p.Source,
		Priority:       p.Priority,
		Name:           p.Name,
		Connections:    nil,
		ConnectionsIDs: p.ConnectionsIDs,
		InstalledById:  p.InstalledById,
	}
	if p.Config != nil {
		p2.Config = &PluginConfig{
			ID:      p.ID,
			Org:     p.OrgID,
			EnvVars: p.Config.EnvVars,
		}
	}
	var connectionList []Connection
	for _, conn := range p.Connections {
		connectionList = append(connectionList, Connection{
			Id:           conn.ID,
			ConnectionId: conn.ConnectionID,
			Name:         conn.Name,
			Config:       conn.Config,
			Groups:       nil, // isn't used
		})
	}
	p2.Connections = connectionList
	return p2
}

func parseToV2(p *Plugin) *types.Plugin {
	v2 := &types.Plugin{OrgID: p.OrgId, Name: p.Name}
	if p.Config != nil {
		if len(p.Config.EnvVars) > 0 {
			v2.Config = &types.PluginConfig{EnvVars: p.Config.EnvVars}
		}
	}
	for _, conn := range p.Connections {
		v2.Connections = append(v2.Connections, types.PluginConnection{
			ID:           conn.Id,
			ConnectionID: conn.ConnectionId,
			Name:         conn.Name,
			Config:       conn.Config,
		})
	}
	return v2
}

func processOnUpdatePluginPhase(registeredPlugins []plugintypes.Plugin, oldState, newState *types.Plugin) error {
	for _, pl := range registeredPlugins {
		if pl.Name() != newState.Name {
			continue
		}
		if err := pl.OnUpdate(oldState, newState); err != nil {
			return err
		}
	}
	return nil
}
