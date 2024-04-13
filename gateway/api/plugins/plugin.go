package apiplugins

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/proto"
	pgconnections "github.com/runopsio/hoop/gateway/pgrest/connections"
	pgplugins "github.com/runopsio/hoop/gateway/pgrest/plugins"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
)

type PluginConnectionRequest struct {
	Name         string   `json:"name"`
	ConnectionID string   `json:"id"`
	Config       []string `json:"config"`
}

type PluginRequest struct {
	Name        string                     `json:"name"        binding:"required"`
	Connections []*PluginConnectionRequest `json:"connections" binding:"required"`
	Config      *types.PluginConfig        `json:"config"`
	Source      *string                    `json:"source"`
	Priority    int                        `json:"priority"`
}

func Post(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req PluginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	existingPlugin, err := pgplugins.New().FetchOne(ctx, req.Name)
	if err != nil {
		log.Errorf("failed retrieving existing plugin, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	if existingPlugin != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "plugin already enabled."})
		return
	}

	newPlugin := types.Plugin{
		ID:            uuid.NewString(),
		OrgID:         ctx.OrgID,
		Name:          req.Name,
		Connections:   nil,
		Config:        nil,
		Source:        req.Source,
		Priority:      req.Priority,
		InstalledById: ctx.UserID,
	}
	if req.Config != nil {
		newPlugin.Config.OrgID = ctx.OrgID
		newPlugin.ConfigID = func() *string { v := uuid.NewString(); return &v }()
		newPlugin.Config = req.Config
		if err := validatePluginConfig(req.Config.EnvVars); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
			return
		}
	}

	pluginConnectionList, pluginConnectionIDs, err := parsePluginConnections(c, req)
	if err != nil {
		return
	}
	newPlugin.ConnectionsIDs = pluginConnectionIDs
	newPlugin.Connections = pluginConnectionList

	if err := processOnUpdatePluginPhase(nil, &newPlugin); err != nil {
		msg := fmt.Sprintf("failed initializing plugin, reason=%v", err)
		log.Errorf(msg)
		sentry.CaptureException(err)
		c.JSON(http.StatusBadRequest, gin.H{"message": msg})
		return
	}
	if err := pgplugins.UpdatePlugin(ctx, &newPlugin); err != nil {
		log.Errorf("failed enabling plugin, reason=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed enabling plugin"})
		return
	}
	redactPluginConfig(newPlugin.Config)
	c.PureJSON(200, &newPlugin)
}

func Put(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req PluginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	existingPlugin, err := pgplugins.New().FetchOne(ctx, req.Name)
	if err != nil {
		log.Errorf("failed retrieving existing plugin, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	if existingPlugin == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
		return
	}

	pluginConnectionList, pluginConnectionIDs, err := parsePluginConnections(c, req)
	if err != nil {
		return
	}

	existingPlugin.Priority = req.Priority
	existingPlugin.Source = req.Source
	existingPlugin.Connections = pluginConnectionList
	existingPlugin.ConnectionsIDs = pluginConnectionIDs
	// avoids creating another pluginconfig document
	// this is kept for compatibility with webapp
	pluginConfig := existingPlugin.Config
	existingPlugin.Config = nil
	if err := pgplugins.UpdatePlugin(ctx, existingPlugin); err != nil {
		log.Errorf("failed updating plugin, reason=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed updating plugin"})
		return
	}
	existingPlugin.Config = pluginConfig
	c.PureJSON(200, existingPlugin)
}

func PutConfig(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	pluginName := c.Param("name")
	var envVars map[string]string
	if err := c.ShouldBindJSON(&envVars); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	existingPlugin, err := pgplugins.New().FetchOne(ctx, pluginName)
	if err != nil {
		log.Errorf("failed retrieving existing plugin, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	if existingPlugin == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
		return
	}

	if err := validatePluginConfig(envVars); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}

	pluginDocID := uuid.NewString()
	pluginConfig := &types.PluginConfig{OrgID: ctx.OrgID, ID: pluginDocID, EnvVars: envVars}
	if existingPlugin.Config != nil {
		// keep the same configuration id to avoid creating a new document
		pluginConfig.ID = *existingPlugin.ConfigID
	}

	newState := newPluginUpdateConfigState(existingPlugin, pluginConfig)
	if err := processOnUpdatePluginPhase(existingPlugin, newState); err != nil {
		msg := fmt.Sprintf("failed initializing plugin, reason=%v", err)
		log.Errorf(msg)
		sentry.CaptureException(err)
		c.JSON(http.StatusBadRequest, gin.H{"message": msg})
		return
	}
	existingPlugin.ConfigID = &pluginConfig.ID
	existingPlugin.Config = pluginConfig
	if err := pgplugins.UpdatePlugin(ctx, existingPlugin); err != nil {
		log.Errorf("failed updating plugin configuration, reason=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed updating plugin configuration"})
		return
	}
	c.PureJSON(200, existingPlugin)
}

func Get(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	name := c.Param("name")
	obj, err := pgplugins.New().FetchOne(ctx, name)
	if err != nil {
		log.Errorf("failed obtaining plugin, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if obj == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "plugin not found"})
		return
	}
	redactPluginConfig(obj.Config)
	c.PureJSON(200, obj)
}

func List(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	itemList, err := pgplugins.New().FetchAll(ctx)
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

func parsePluginConnections(c *gin.Context, req PluginRequest) ([]*types.PluginConnection, []string, error) {
	ctx := storagev2.ParseContext(c)
	// remove repeated connection request
	dedupePluginConnectionRequest := map[string]*PluginConnectionRequest{}
	for _, conn := range req.Connections {
		dedupePluginConnectionRequest[conn.ConnectionID] = conn
	}
	var connectionIDList []string
	for connID := range dedupePluginConnectionRequest {
		connectionIDList = append(connectionIDList, connID)
	}
	connectionsMap, err := pgconnections.New().FetchByIDs(ctx, connectionIDList)
	if err != nil {
		log.Errorf("failed retrieving existing plugin, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return nil, nil, io.EOF
	}
	pluginConnectionList := []*types.PluginConnection{}
	var connectionIDs []string
	// validate if connection exists in the store
	for _, reqconn := range dedupePluginConnectionRequest {
		conn, ok := connectionsMap[reqconn.ConnectionID]
		if !ok {
			msg := fmt.Sprintf("connection %q doesn't exists", reqconn.ConnectionID)
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": msg})
			return nil, nil, io.EOF
		}
		connConfig := reqconn.Config
		if req.Name == plugintypes.PluginDLPName && len(connConfig) == 0 {
			connConfig = proto.DefaultInfoTypes
		}
		// create deterministic uuid to allow plugin connection entities
		// to be updated instead of generating new ones
		docUUID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("%s:%s", req.Name, conn.Id)))
		pluginConnectionList = append(pluginConnectionList, &types.PluginConnection{
			ID:           docUUID.String(),
			ConnectionID: conn.Id,
			Name:         conn.Name,
			Config:       connConfig,
		})
		connectionIDs = append(connectionIDs, docUUID.String())
	}
	return pluginConnectionList, connectionIDs, nil
}

func redactPluginConfig(c *types.PluginConfig) {
	if c != nil {
		for envKey := range c.EnvVars {
			c.EnvVars[envKey] = "REDACTED"
		}
	}
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

func processOnUpdatePluginPhase(oldState, newState *types.Plugin) error {
	for _, pl := range plugintypes.RegisteredPlugins {
		if pl.Name() != newState.Name {
			continue
		}
		if err := pl.OnUpdate(oldState, newState); err != nil {
			return err
		}
	}
	return nil
}

func newPluginUpdateConfigState(existingPlugin *types.Plugin, newConfig *types.PluginConfig) *types.Plugin {
	return &types.Plugin{
		ID:             existingPlugin.ID,
		OrgID:          existingPlugin.OrgID,
		Name:           existingPlugin.Name,
		ConnectionsIDs: existingPlugin.ConnectionsIDs,
		Connections:    existingPlugin.Connections,
		ConfigID:       &newConfig.ID,
		Config:         newConfig,
		Source:         existingPlugin.Source,
		Priority:       existingPlugin.Priority,
		InstalledById:  existingPlugin.InstalledById,
	}
}
