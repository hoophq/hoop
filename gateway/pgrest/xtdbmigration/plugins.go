package xtdbmigration

import (
	"github.com/runopsio/hoop/common/log"
	pgconnections "github.com/runopsio/hoop/gateway/pgrest/connections"
	pgplugins "github.com/runopsio/hoop/gateway/pgrest/plugins"
	"github.com/runopsio/hoop/gateway/storagev2"
	pluginstorage "github.com/runopsio/hoop/gateway/storagev2/plugin"
	"github.com/runopsio/hoop/gateway/storagev2/types"
)

func migratePlugins(xtdbURL, orgID string) {
	log.Infof("pgrest migration: migrating plugins")
	ctx := storagev2.NewOrganizationContext(orgID, store)
	ctx.SetURL(xtdbURL)
	pluginList, err := pluginstorage.List(ctx)
	if err != nil {
		log.Infof("pgrest migration: failed listing plugins, err=%v", err)
		return
	}
	var state migrationState
	for _, p := range pluginList {
		exitentPlugin, err := pgplugins.New().FetchOne(ctx, p.Name)
		if err != nil {
			log.Warnf("pgrest migration: failed fetching existent plugin=%v, err=%v", p.Name, err)
			continue
		}
		// the connection needs to exists when creating
		// plugin connections, so we need to filter out
		// the state of plugin connection -> connection may
		// not be consistent.
		var pluginConnections []*types.PluginConnection
		for _, pconn := range p.Connections {
			conn, err := pgconnections.New().FetchOneByNameOrID(ctx, pconn.ConnectionID)
			if err != nil {
				log.Warnf("pgrest migration: failed fetching connection %v, plugin=%v, err=%v", pconn.Name, p.Name, err)
				continue
			}
			if conn == nil {
				log.Warnf("connection %s, id=%s not found, plugin=%v", pconn.Name, pconn.ConnectionID, p.Name)
				continue
			}
			pluginConnections = append(pluginConnections, pconn)
		}
		p.Connections = pluginConnections
		if err := pgplugins.New().Upsert(ctx, exitentPlugin, &p); err != nil {
			log.Warnf("pgrest migration: failed migrating plugin=%v, err=%v", p.Name, err)
			state.failed++
			continue
		}
		state.success++
	}
	log.Infof("pgrest migration: plugins migrated, total=%v, success=%d, failed=%d", len(pluginList), state.success, state.failed)
}
