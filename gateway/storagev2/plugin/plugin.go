package pluginstorage

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	pgplugins "github.com/runopsio/hoop/gateway/pgrest/plugins"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
)

func Put(ctx *storagev2.Context, pl *types.Plugin) error {
	existentPlugin, err := GetByName(ctx, pl.Name)
	if err != nil {
		return fmt.Errorf("failed obtaining existent plugin, err=%v", err)
	}
	return pgplugins.New().Upsert(ctx, existentPlugin, pl)
}

func GetByName(ctx *storagev2.Context, name string) (*types.Plugin, error) {
	return pgplugins.New().FetchOne(ctx, name)
}

func List(ctx *storagev2.Context) ([]types.Plugin, error) {
	return pgplugins.New().FetchAll(ctx)
}

var defaultPlugins = []string{
	plugintypes.PluginAuditName,
	plugintypes.PluginDLPName,
	plugintypes.PluginIndexName,
	plugintypes.PluginEditorName,
	plugintypes.PluginSlackName,
	plugintypes.PluginRunbooksName,
	plugintypes.PluginWebhookName,
}

// EnableDefaultPlugins will enable the default plugins for a connection in a best-effort manner
func EnableDefaultPlugins(ctx *storagev2.Context, connID, connName string) {
	for _, name := range defaultPlugins {
		pl, err := GetByName(ctx, name)
		if err != nil {
			log.Warnf("failed fetching plugin %v, reason=%v", name, err)
			continue
		}
		var connConfig []string
		if name == plugintypes.PluginDLPName {
			connConfig = pb.DefaultInfoTypes
		}
		if pl == nil {
			docID := uuid.NewString()
			newPlugin := &types.Plugin{
				ID:    uuid.NewString(),
				OrgID: ctx.OrgID,
				Name:  name,
				Connections: []*types.PluginConnection{{
					ID:           docID,
					ConnectionID: connID,
					Name:         connName,
					Config:       connConfig,
				}},
				ConnectionsIDs: []string{docID},
				InstalledById:  ctx.UserID,
			}
			if err := Put(ctx, newPlugin); err != nil {
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
		if !enabled {
			docID := uuid.NewString()
			pl.ConnectionsIDs = append(pl.ConnectionsIDs, docID)
			pluginConnection := &types.PluginConnection{
				ID:           docID,
				ConnectionID: connID,
				Name:         connName,
				Config:       connConfig,
			}

			pl.Connections = append(pl.Connections, pluginConnection)
			if err := Put(ctx, pl); err != nil {
				log.Warnf("failed enabling plugin %v, reason=%v", pl.Name, err)
			}
		}
	}
}
