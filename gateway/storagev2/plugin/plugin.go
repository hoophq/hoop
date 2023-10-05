package pluginstorage

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
	"olympos.io/encoding/edn"
)

func Put(ctx *storagev2.Context, pl *types.Plugin) error {
	txList := []types.TxObject{
		&struct {
			DocID          string   `edn:"xt/id"`
			OrgID          string   `edn:"plugin/org"`
			Name           string   `edn:"plugin/name"`
			ConnectionsIDs []string `edn:"plugin/connection-ids"`
			ConfigID       *string  `edn:"plugin/config-id"`
			Source         *string  `edn:"plugin/source"`
			Priority       int      `edn:"plugin/priority"`
			InstalledById  string   `edn:"plugin/installed-by"`
		}{
			DocID:          pl.ID,
			OrgID:          pl.OrgID,
			Name:           pl.Name,
			ConnectionsIDs: pl.ConnectionsIDs,
			ConfigID:       pl.ConfigID,
			Source:         pl.Source,
			Priority:       pl.Priority,
			InstalledById:  pl.InstalledById,
		},
	}
	for _, conn := range pl.Connections {
		txList = append(txList, &struct {
			DocID        string   `edn:"xt/id"`
			ConnectionID string   `edn:"plugin-connection/id"`
			Name         string   `edn:"plugin-connection/name"`
			Config       []string `edn:"plugin-connection/config"`
		}{
			DocID:        conn.ID,
			ConnectionID: conn.ConnectionID,
			Name:         conn.Name,
			Config:       conn.Config,
		})
	}
	if pl.Config != nil {
		txList = append(txList, pl.Config)
	}
	_, err := ctx.Put(txList...)
	return err
}

func GetByName(ctx *storagev2.Context, name string) (*types.Plugin, error) {
	payload := fmt.Sprintf(`{:query {
		:find [(pull ?p
			[:xt/id
            :plugin/org
            :plugin/name
            :plugin/source
            :plugin/priority
            :plugin/installed-by
            :plugin/config-id
            :plugin/connection-ids
            {(:plugin/config-id {:as :plugin/config}) [:xt/id :pluginconfig/envvars]}
            {(:plugin/connection-ids {:as :plugin/connections}) 
									[:xt/id
									 :plugin-connection/id
                                     :plugin-connection/name
                                     :plugin-connection/config
									 {(:plugin-connection/id {:as :connection}) [:connection/name]}]}])]
		:in [orgid name]
		:where [[?p :plugin/org orgid]
                [?p :plugin/name name]]}
		:in-args [%q %q]}`, ctx.OrgID, name)
	data, err := ctx.Query(payload)
	if err != nil {
		return nil, err
	}
	var p [][]types.Plugin
	if err := edn.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	if len(p) == 0 {
		return nil, nil
	}
	// filter evicted connections that returns as nil
	var pluginConnectionList []*types.PluginConnection
	for _, c := range p[0][0].Connections {
		if c == nil {
			continue
		}
		// fixes plugin-connection/name attribute that is not enforced properly
		c.SetName()
		pluginConnectionList = append(pluginConnectionList, c)
	}
	p[0][0].Connections = pluginConnectionList
	// return empty list instead of null
	if p[0][0].Connections == nil {
		p[0][0].Connections = []*types.PluginConnection{}
	}
	return &p[0][0], nil
}

func List(ctx *storagev2.Context) ([]types.Plugin, error) {
	payload := fmt.Sprintf(`{:query {
		:find [(pull ?p
			[:xt/id
            :plugin/org
            :plugin/name
            :plugin/source
            :plugin/priority
            :plugin/installed-by
            :plugin/config-id
            :plugin/connection-ids
            {(:plugin/config-id {:as :plugin/config}) [:xt/id :pluginconfig/envvars]}
            {(:plugin/connection-ids {:as :plugin/connections}) 
									[:xt/id
									 :plugin-connection/id
                                     :plugin-connection/name                                     
                                     :plugin-connection/config
									 {(:plugin-connection/id {:as :connection}) [:connection/name]}]}])]
		:in [orgid]
		:where [[?p :plugin/org orgid]]}
		:in-args [%q]}`, ctx.OrgID)
	data, err := ctx.Query(payload)
	if err != nil {
		return nil, err
	}
	var plugins [][]types.Plugin
	if err := edn.Unmarshal(data, &plugins); err != nil {
		return nil, err
	}

	var itemList []types.Plugin
	for _, p := range plugins {
		// filter evicted connections that returns as nil
		var pluginConnectionList []*types.PluginConnection
		for _, c := range p[0].Connections {
			if c == nil {
				continue
			}
			// fixes plugin-connection/name attribute
			// that is not enforced properly
			c.SetName()
			pluginConnectionList = append(pluginConnectionList, c)
		}
		p[0].Connections = pluginConnectionList
		// return empty list instead of null
		if p[0].Connections == nil {
			p[0].Connections = []*types.PluginConnection{}
		}
		itemList = append(itemList, p[0])
	}

	return itemList, nil
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
				log.Warnf("failed creating plugin %v, reason=%v", pl.Name, err)
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
