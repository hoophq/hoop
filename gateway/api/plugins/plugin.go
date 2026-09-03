package apiplugins

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
)

// TODO: move to openapi
type PluginConnectionRequest struct {
	Name         string   `json:"name"`
	ConnectionID string   `json:"id"`
	Config       []string `json:"config"`
}

// TODO: move to openapi
type PluginRequest struct {
	Name        string                     `json:"name"        binding:"required"`
	Connections []*PluginConnectionRequest `json:"connections" binding:"required"`
	Config      *types.PluginConfig        `json:"config"`
}

// controlPlanePlugin is the only plugin the control plane administers. Slack is
// where review approvals are delivered; the rest (dlp, review, audit,
// access_control, webhooks, runbooks, indexer) configure the gateway's session
// pipeline, which a control plane does not run.
const controlPlanePlugin = "slack"

// hiddenOnControlPlane answers 404 for any plugin other than slack when the
// process serves the control-plane surface.
//
// The route allow-list in gateway/api/apiroutes/controlplane.go cannot express this:
// /plugins is one route for every plugin, and the name arrives in the body or a
// path param. This is the case the APP_MODE flag exists for. The response
// matches a blocked route exactly, so the two are indistinguishable to a caller.
func hiddenOnControlPlane(c *gin.Context, pluginName string) bool {
	if !appconfig.Get().IsControlPlane() || pluginName == controlPlanePlugin {
		return false
	}
	c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	return true
}

// CreatePlugin
//
//	@Summary		Create Plugin
//	@Description	Create Plugin resource
//	@Tags			Plugins
//	@Accept			json
//	@Produce		json
//	@Param			request		body		openapi.Plugin	true	"The request body resource"
//	@Success		201			{object}	openapi.Plugin
//	@Failure		400,422,500	{object}	openapi.HTTPError
//	@Router			/plugins [post]
func Post(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	// TODO: refactor to use openapi.Plugin type
	var req PluginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	if hiddenOnControlPlane(c, req.Name) {
		return
	}

	// existingPlugin, err := pgplugins.New().FetchOne(ctx, req.Name)
	_, err := models.GetPluginByName(models.DB, ctx.OrgID, req.Name)
	switch err {
	case models.ErrNotFound:
	case nil:
		c.JSON(http.StatusBadRequest, gin.H{"message": "plugin already enabled"})
		return
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed retrieving existing plugin: %v", err)
		return
	}

	pluginID := uuid.NewString()
	newPlugin := &models.Plugin{
		ID:          pluginID,
		OrgID:       ctx.OrgID,
		Name:        req.Name,
		Connections: parsePluginConnections(c, pluginID, req),
		EnvVars:     nil,
	}
	if req.Config != nil {
		if err := validatePluginConfig(req.Config.EnvVars); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
			return
		}
		newPlugin.EnvVars = req.Config.EnvVars
	}

	if err := processOnUpdatePluginPhase(nil, newPlugin); err != nil {
		msg := fmt.Sprintf("failed initializing plugin, reason=%v", err)
		log.Errorf(msg)
		c.JSON(http.StatusBadRequest, gin.H{"message": msg})
		return
	}

	if err := models.UpsertPlugin(newPlugin); err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating plugin %v, reason=%v", req.Name, err)
		return
	}
	c.JSON(http.StatusCreated, toOpenApi(newPlugin))
}

// UpdatePlugin
//
//	@Summary		Update Plugin
//	@Description	Update Plugin resource. The `config` and `name` attributes are immutable for this endpoint.
//	@Tags			Plugins
//	@Accept			json
//	@Produce		json
//	@Param			name		path		string			true	"The name of the resource"
//	@Param			request		body		openapi.Plugin	true	"The request body resource"
//	@Success		200			{object}	openapi.Plugin
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/plugins/{name} [put]
func Put(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req PluginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	if hiddenOnControlPlane(c, req.Name) {
		return
	}

	existingPlugin, err := models.GetPluginByName(models.DB, ctx.OrgID, req.Name)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "plugin not found"})
		return
	case nil:
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed retrieving existing plugin")
		return
	}
	existingPlugin.Connections = parsePluginConnections(c, existingPlugin.ID, req)
	// avoids creating another pluginconfig document
	// this is kept for compatibility with webapp
	existingPlugin.EnvVars = nil
	if err := models.UpsertPlugin(existingPlugin); err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed updating plugin")
		return
	}
	c.JSON(http.StatusOK, toOpenApi(existingPlugin))
}

// UpdatePluginConfig
//
//	@Summary		Update Plugin Config
//	@Description	Update the Plugin resource top level configuration.
//	@Tags			Plugins
//	@Accept			json
//	@Produce		json
//	@Param			name			path		string					true	"The name of the plugin"
//	@Param			request			body		openapi.PluginConfig	true	"The request body resource"
//	@Success		200				{object}	openapi.Plugin
//	@Failure		400,404,422,500	{object}	openapi.HTTPError
//	@Router			/plugins/{name}/config [put]
func PutConfig(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	pluginName := c.Param("name")
	if hiddenOnControlPlane(c, pluginName) {
		return
	}
	var envVars map[string]string
	if err := c.ShouldBindJSON(&envVars); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	existingPlugin, err := models.GetPluginByName(models.DB, ctx.OrgID, pluginName)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "plugin not found"})
		return
	case nil:
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed retrieving existing plugin: %v", err)
		return
	}

	if err := validatePluginConfig(envVars); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}

	newState := newPluginUpdateConfigState(existingPlugin, envVars)
	if err := processOnUpdatePluginPhase(existingPlugin, newState); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Sprintf("failed initializing plugin, reason=%v", err)})
		return
	}

	existingPlugin.EnvVars = envVars
	err = models.UpsertEnvVar(models.DB, &models.EnvVar{
		OrgID:     existingPlugin.OrgID,
		ID:        existingPlugin.ID,
		Envs:      envVars,
		UpdatedAt: time.Now().UTC(),
	})

	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed updating plugin configuration: %v", err)
		return
	}
	c.JSON(http.StatusOK, toOpenApi(existingPlugin))
}

// GetPlugin
//
//	@Summary		Get Plugin
//	@Description	Get a plugin resource by name
//	@Tags			Plugins
//	@Produce		json
//	@Param			name	path		string	true	"The name of the plugin"
//	@Success		200		{object}	openapi.Plugin
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/plugins/{name} [get]
func Get(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	name := c.Param("name")
	if hiddenOnControlPlane(c, name) {
		return
	}
	obj, err := models.GetPluginByName(models.DB, ctx.OrgID, name)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "plugin not found"})
		return
	case nil:
		c.JSON(http.StatusOK, toOpenApi(obj))
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed obtaining plugin: %v", err)
		return
	}
}

// ListPlugins
//
//	@Summary		List Plugins
//	@Description	List all Plugin resources
//	@Tags			Plugins
//	@Produce		json
//	@Success		200	{array}		openapi.Plugin
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/plugins [get]
func List(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	itemList, err := models.ListPlugins(ctx.OrgID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing plugins: %v", err)
		return
	}
	isControlPlane := appconfig.Get().IsControlPlane()
	var out []*openapi.Plugin
	for _, p := range itemList {
		// Listing has no name in the request, so the filter is applied to the
		// result instead of refused up front. Without it a control-plane admin
		// sees every gateway plugin and can read which are enabled.
		if isControlPlane && p.Name != controlPlanePlugin {
			continue
		}
		plugin := toOpenApi(&p)
		if config := plugin.Config; config != nil {
			for key := range config.EnvVars {
				config.EnvVars[key] = "REDACTED"
			}
		}
		out = append(out, &plugin)
	}
	c.JSON(http.StatusOK, out)
}

func parsePluginConnections(c *gin.Context, pluginID string, req PluginRequest) []*models.PluginConnection {
	ctx := storagev2.ParseContext(c)
	// remove repeated connection request
	dedupeTracking := map[string]any{}
	var pluginConnectionList []*models.PluginConnection
	for _, conn := range req.Connections {
		if _, ok := dedupeTracking[conn.ConnectionID]; ok {
			continue
		}
		dedupeTracking[conn.ConnectionID] = nil
		pluginConnectionList = append(pluginConnectionList, &models.PluginConnection{
			ID:             uuid.NewString(),
			OrgID:          ctx.OrgID,
			PluginID:       pluginID,
			ConnectionID:   conn.ConnectionID,
			ConnectionName: "",
			Config:         conn.Config,
			Enabled:        true,
			CreatedAt:      time.Now().UTC(),
			UpdatedAt:      time.Now().UTC(),
		})
	}
	return pluginConnectionList
}

func validatePluginConfig(configEnvVars map[string]string) error {
	if len(configEnvVars) == 0 {
		return nil
	}
	for key, val := range configEnvVars {
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

func processOnUpdatePluginPhase(oldState, newState plugintypes.PluginResource) error {
	for _, pl := range plugintypes.RegisteredPlugins {
		if pl.Name() != newState.GetName() {
			continue
		}
		if err := pl.OnUpdate(oldState, newState); err != nil {
			return err
		}
	}
	return nil
}

func toOpenApi(obj *models.Plugin) openapi.Plugin {
	connections := make([]*openapi.PluginResourceConnection, len(obj.Connections))
	for i, pconn := range obj.Connections {
		connections[i] = &openapi.PluginResourceConnection{
			ConnectionID: pconn.ConnectionID,
			Name:         pconn.ConnectionName,
			Config:       pconn.Config,
		}
	}
	plugin := openapi.Plugin{
		ID:          obj.ID,
		Name:        obj.Name,
		Connections: connections,
		Source:      nil, // deprecated
		Priority:    0,   // deprecated
		Config:      nil,
	}
	if len(obj.EnvVars) > 0 {
		plugin.Config = &openapi.PluginConfig{
			ID:      obj.ID,
			EnvVars: obj.EnvVars}
	}
	return plugin
}

func newPluginUpdateConfigState(existingPlugin *models.Plugin, envVars map[string]string) *models.Plugin {
	return &models.Plugin{
		ID:          existingPlugin.ID,
		OrgID:       existingPlugin.OrgID,
		Name:        existingPlugin.Name,
		Connections: existingPlugin.Connections,
		EnvVars:     envVars,
	}
}
