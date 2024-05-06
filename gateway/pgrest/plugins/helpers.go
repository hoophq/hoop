package pgplugins

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/pgrest"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
)

var DefaultPluginNames = []string{
	plugintypes.PluginAuditName,
	plugintypes.PluginDLPName,
	plugintypes.PluginIndexName,
	plugintypes.PluginEditorName,
	plugintypes.PluginSlackName,
	plugintypes.PluginRunbooksName,
	plugintypes.PluginWebhookName,
}

func UpdatePlugin(ctx pgrest.OrgContext, pl *types.Plugin) error {
	existentPlugin, err := New().FetchOne(ctx, pl.Name)
	if err != nil {
		return fmt.Errorf("failed obtaining existent plugin, err=%v", err)
	}
	return New().Upsert(ctx, existentPlugin, pl)
}

func EnableDefaultPlugins(ctx pgrest.LicenseContext, connID, connName string, pluginList []string) {
	for _, name := range pluginList {
		pl, err := New().FetchOne(ctx, name)
		if err != nil {
			log.Warnf("failed fetching plugin %v, reason=%v", name, err)
			continue
		}
		var connConfig []string
		if name == plugintypes.PluginDLPName {
			connConfig = proto.DefaultInfoTypes
		}
		if pl == nil {
			docID := uuid.NewString()
			newPlugin := &types.Plugin{
				ID:    uuid.NewString(),
				OrgID: ctx.GetOrgID(),
				Name:  name,
				Connections: []*types.PluginConnection{{
					ID:           docID,
					ConnectionID: connID,
					Name:         connName,
					Config:       connConfig,
				}},
				ConnectionsIDs: []string{docID},
			}
			if !pgrest.IsValidLicense(ctx, name) {
				newPlugin.Connections = nil
			}
			if err := UpdatePlugin(ctx, newPlugin); err != nil {
				log.Warnf("failed creating plugin %v, reason=%v", name, err)
			}
			continue
		}
		var enabled bool
		for _, conn := range pl.Connections {
			if conn.ConnectionID == connID {
				enabled = true
				break
			}
		}
		if !enabled && pgrest.IsValidLicense(ctx, name) {

			docID := uuid.NewString()
			pl.ConnectionsIDs = append(pl.ConnectionsIDs, docID)
			pluginConnection := &types.PluginConnection{
				ID:           docID,
				ConnectionID: connID,
				Name:         connName,
				Config:       connConfig,
			}

			pl.Connections = append(pl.Connections, pluginConnection)
			if err := UpdatePlugin(ctx, pl); err != nil {
				log.Warnf("failed enabling plugin %v, reason=%v", pl.Name, err)
			}
		}
	}
}

// UpsertPluginConnection will create or update the plugin connection for the target plugin
// if the plugin does not exist it will be created.
// this function shouldn't be used if the plugin uses the config field (e.g.: slack)
func UpsertPluginConnection(ctx pgrest.LicenseContext, pluginName string, pluginConn *types.PluginConnection) {
	existentPlugin, err := New().FetchOne(ctx, pluginName)
	if err != nil {
		log.Warnf("failed fetching plugin %v, reason=%v", pluginName, err)
		return
	}
	newPlugin := &types.Plugin{
		ID:             uuid.NewString(),
		OrgID:          ctx.GetOrgID(),
		Name:           pluginName,
		Connections:    []*types.PluginConnection{pluginConn},
		ConnectionsIDs: nil,
		// Config is not used by dlp and review plugins
		// it's safe to override it.
		// Change this if the plugin starts using the config field
		Config: nil,
	}
	if existentPlugin == nil {
		// don't enable the plugin if the config is empty
		if len(pluginConn.Config) == 0 {
			return
		}
		if !pgrest.IsValidLicense(ctx, pluginName) {
			newPlugin.Connections = nil
		}
		if err := New().Upsert(ctx, nil, newPlugin); err != nil {
			log.Warnf("failed creating plugin %v, reason=%v", pluginName, err)
		}
		return
	}

	// it's not a valid license, don't proceed
	if !pgrest.IsValidLicense(ctx, pluginName) {
		return
	}

	newPlugin.ID = existentPlugin.ID
	newPlugin.OrgID = existentPlugin.OrgID
	newPlugin.Connections = existentPlugin.Connections

	switch {
	// remove the plugin connection if the config is empty
	case len(pluginConn.Config) == 0:
		newPlugin.Connections = filterPluginConnection(newPlugin.Connections, pluginConn.ConnectionID)
	default:
		var enabled bool
		for _, conn := range newPlugin.Connections {
			if conn.ConnectionID == pluginConn.ConnectionID {
				enabled = true
				// mutate the config field if the plugin connection exits
				conn.Config = pluginConn.Config
				break
			}
		}
		// add the plugin connection if it does not exist
		if !enabled {
			newPlugin.Connections = append(newPlugin.Connections, pluginConn)
		}
	}

	if err := New().Upsert(ctx, existentPlugin, newPlugin); err != nil {
		log.Warnf("failed updating plugin %v, reason=%v", pluginName, err)
	}
}

func filterPluginConnection(pluginConnList []*types.PluginConnection, connID string) []*types.PluginConnection {
	filtered := []*types.PluginConnection{}
	for _, conn := range pluginConnList {
		if conn.ConnectionID != connID {
			filtered = append(filtered, conn)
		}
	}
	return filtered
}
