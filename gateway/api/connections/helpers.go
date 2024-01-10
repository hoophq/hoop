package apiconnections

import (
	"slices"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/gateway/pgrest"
	pgplugins "github.com/runopsio/hoop/gateway/pgrest/plugins"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
)

func accessControlAllowed(ctx pgrest.Context) (func(connName string) bool, error) {
	p, err := pgplugins.New().FetchOne(ctx, plugintypes.PluginAccessControlName)
	if err != nil {
		return nil, err
	}
	if p == nil || ctx.IsAdmin() {
		return func(_ string) bool { return true }, nil
	}

	return func(connName string) (allow bool) {
		for _, c := range p.Connections {
			if c.Name == connName {
				for _, userGroup := range ctx.GetUserGroups() {
					allow = slices.Contains(c.Config, userGroup)
				}
				return allow
			}
		}
		return false
	}, nil
}

// upsertPluginConnection will create or update the plugin connection for the target plugin
// if the plugin does not exist it will be created.
// this function shouldn't be used if the plugin uses the config field (e.g.: slack)
func upsertPluginConnection(ctx pgrest.Context, pluginName string, pluginConn *types.PluginConnection) {
	existentPlugin, err := pgplugins.New().FetchOne(ctx, pluginName)
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
		if err := pgplugins.New().Upsert(ctx, nil, newPlugin); err != nil {
			log.Warnf("failed creating plugin %v, reason=%v", pluginName, err)
		}
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

	if err := pgplugins.New().Upsert(ctx, existentPlugin, newPlugin); err != nil {
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
