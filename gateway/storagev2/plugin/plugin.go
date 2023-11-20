package pluginstorage

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/pgrest"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
)

func Put(ctx *storagev2.Context, pl *types.Plugin) (err error) {
	existentPlugin, err := GetByName(ctx, pl.Name)
	if err != nil {
		return fmt.Errorf("failed obtaining existent plugin, err=%v", err)
	}
	pluginConnections := map[string]*pgrest.PluginConnection{}
	pluginID := pl.ID
	if existentPlugin != nil {
		pluginID = existentPlugin.ID
		for _, plconn := range existentPlugin.Connections {
			pluginConnections[plconn.ConnectionID] = &pgrest.PluginConnection{
				ID:               plconn.ID,
				OrgID:            pl.OrgID,
				PluginID:         pluginID,
				ConnectionID:     plconn.ConnectionID,
				Enabled:          false,
				ConnectionConfig: []string{},
			}
		}
	} else {
		err = pgrest.New("/plugins").Create(map[string]any{
			"id":     pl.ID,
			"org_id": pl.OrgID,
			"name":   pl.Name,
			"source": pl.Source,
		}).Error()
		if err != nil {
			return fmt.Errorf("failed creating plugin %v, reason=%v", pl.Name, err)
		}
	}
	if pl.Config != nil {
		err = pgrest.New("/env_vars").Upsert(map[string]any{
			"id":     pl.ID,
			"org_id": pl.OrgID,
			"envs":   pl.Config.EnvVars,
		}).Error()
		if err != nil {
			return err
		}
	}
	for _, plconn := range pl.Connections {
		pluginConnections[plconn.ConnectionID] = &pgrest.PluginConnection{
			ID:               plconn.ID,
			OrgID:            pl.OrgID,
			PluginID:         pluginID,
			ConnectionID:     plconn.ConnectionID,
			Enabled:          true,
			ConnectionConfig: plconn.Config,
		}
	}
	var reqBody []map[string]any
	for _, plconn := range pluginConnections {
		reqBody = append(reqBody, map[string]any{
			"id":            plconn.ID,
			"org_id":        pl.OrgID,
			"plugin_id":     pluginID,
			"connection_id": plconn.ConnectionID,
			"enabled":       plconn.Enabled,
			"config":        plconn.ConnectionConfig,
		})
	}

	// batch request
	err = pgrest.New("/plugin_connections?on_conflict=plugin_id,connection_id").
		Upsert(reqBody).
		Error()
	if err != nil {
		return err
	}
	// best effort, delete all non-enabled plugin connections
	_ = pgrest.New("/plugin_connections?org_id=eq.%s&enabled=is.false", pl.OrgID).Delete()
	return
	// txList := []types.TxObject{
	// 	&struct {
	// 		DocID          string   `edn:"xt/id"`
	// 		OrgID          string   `edn:"plugin/org"`
	// 		Name           string   `edn:"plugin/name"`
	// 		ConnectionsIDs []string `edn:"plugin/connection-ids"`
	// 		ConfigID       *string  `edn:"plugin/config-id"`
	// 		Source         *string  `edn:"plugin/source"`
	// 		Priority       int      `edn:"plugin/priority"`
	// 		InstalledById  string   `edn:"plugin/installed-by"`
	// 	}{
	// 		DocID:          pl.ID,
	// 		OrgID:          pl.OrgID,
	// 		Name:           pl.Name,
	// 		ConnectionsIDs: pl.ConnectionsIDs,
	// 		ConfigID:       pl.ConfigID,
	// 		Source:         pl.Source,
	// 		Priority:       pl.Priority,
	// 		InstalledById:  pl.InstalledById,
	// 	},
	// }
	// for _, conn := range pl.Connections {
	// 	txList = append(txList, &struct {
	// 		DocID        string   `edn:"xt/id"`
	// 		ConnectionID string   `edn:"plugin-connection/id"`
	// 		Name         string   `edn:"plugin-connection/name"`
	// 		Config       []string `edn:"plugin-connection/config"`
	// 	}{
	// 		DocID:        conn.ID,
	// 		ConnectionID: conn.ConnectionID,
	// 		Name:         conn.Name,
	// 		Config:       conn.Config,
	// 	})
	// }
	// if pl.Config != nil {
	// 	txList = append(txList, pl.Config)
	// }
	// _, err := ctx.Put(txList...)
	// return err
}

func GetByName(ctx *storagev2.Context, name string) (*types.Plugin, error) {
	var pl pgrest.Plugin
	if err := pgrest.New("/plugins?select=*,env_vars(id,envs)&org_id=eq.%v&name=eq.%s", ctx.OrgID, name).
		FetchOne().
		DecodeInto(&pl); err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	resp := types.Plugin{
		ID:            pl.ID,
		OrgID:         pl.OrgID,
		Name:          pl.Name,
		Source:        pl.Source,
		Connections:   []*types.PluginConnection{},
		Priority:      0,
		InstalledById: "",
	}
	if pl.EnvVar != nil {
		resp.ConfigID = &pl.EnvVar.ID
		resp.Config = &types.PluginConfig{
			ID:      pl.EnvVar.ID,
			OrgID:   pl.OrgID,
			EnvVars: pl.EnvVar.Envs}
	}
	var pgPlConnections []pgrest.PluginConnection
	err := pgrest.New("/plugin_connections?select=*,connections(id,name),env_vars(id,envs),plugins!inner(id,name)&org_id=eq.%s&enabled=is.true&plugins.name=eq.%s",
		ctx.OrgID, name).
		List().
		DecodeInto(&pgPlConnections)
	if err != nil {
		if err == pgrest.ErrNotFound {
			return &resp, nil
		}
		return nil, err
	}
	for _, pc := range pgPlConnections {
		resp.Connections = append(resp.Connections, &types.PluginConnection{
			ID:           pc.ID,
			ConnectionID: pc.ConnectionID,
			Name:         pc.Connection.Name,
			Config:       pc.ConnectionConfig,
		})
	}
	return &resp, nil

	// pgrest.New("/plugin_connections?on_conflict=plugin_id,connection_id&select=*,connections(id,name),env_vars(id,envs),plugins(id,name)").
	// 	Create()
	// return nil, nil
	// payload := fmt.Sprintf(`{:query {
	// 	:find [(pull ?p
	// 		[:xt/id
	//         :plugin/org
	//         :plugin/name
	//         :plugin/source
	//         :plugin/priority
	//         :plugin/installed-by
	//         :plugin/config-id
	//         :plugin/connection-ids
	//         {(:plugin/config-id {:as :plugin/config}) [:xt/id :pluginconfig/envvars]}
	//         {(:plugin/connection-ids {:as :plugin/connections})
	// 								[:xt/id
	// 								 :plugin-connection/id
	//                                  :plugin-connection/name
	//                                  :plugin-connection/config
	// 								 {(:plugin-connection/id {:as :connection}) [:connection/name]}]}])]
	// 	:in [orgid name]
	// 	:where [[?p :plugin/org orgid]
	//             [?p :plugin/name name]]}
	// 	:in-args [%q %q]}`, ctx.OrgID, name)
	// data, err := ctx.Query(payload)
	// if err != nil {
	// 	return nil, err
	// }
	// var p [][]types.Plugin
	// if err := edn.Unmarshal(data, &p); err != nil {
	// 	return nil, err
	// }
	// if len(p) == 0 {
	// 	return nil, nil
	// }
	// // filter evicted connections that returns as nil
	// var pluginConnectionList []*types.PluginConnection
	// for _, c := range p[0][0].Connections {
	// 	if c == nil {
	// 		continue
	// 	}
	// 	// fixes plugin-connection/name attribute that is not enforced properly
	// 	c.SetName()
	// 	pluginConnectionList = append(pluginConnectionList, c)
	// }
	// p[0][0].Connections = pluginConnectionList
	// // return empty list instead of null
	// if p[0][0].Connections == nil {
	// 	p[0][0].Connections = []*types.PluginConnection{}
	// }
	// return &p[0][0], nil
}

func List(ctx *storagev2.Context) ([]types.Plugin, error) {
	var pgplugins []pgrest.Plugin
	err := pgrest.New("/plugins?select=*,env_vars(id,envs)&org_id=eq.%v", ctx.OrgID).
		List().
		DecodeInto(&pgplugins)
	if err != nil {
		if err != pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}

	var pgPlConnections []pgrest.PluginConnection
	err = pgrest.New("/plugin_connections?select=*,connections(id,name),env_vars(id,envs),plugins(id,name)&org_id=eq.%s&enabled=is.true)", ctx.OrgID).
		List().
		DecodeInto(&pgPlConnections)
	if err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}

	var plugins []types.Plugin
	for _, p := range pgplugins {
		pluginConnections := []*types.PluginConnection{}
		for _, pc := range pgPlConnections {
			if pc.Plugin.Name == p.Name {
				pluginConnections = append(pluginConnections, &types.PluginConnection{
					ID:           pc.ID,
					ConnectionID: pc.ConnectionID,
					Name:         pc.Connection.Name,
					Config:       pc.ConnectionConfig,
				})
				break
			}
		}
		plugin := types.Plugin{
			ID:             p.ID,
			OrgID:          p.OrgID,
			Name:           p.Name,
			Connections:    pluginConnections,
			ConnectionsIDs: []string{},
			Source:         p.Source,
			Priority:       0,
			InstalledById:  "",
		}
		if p.EnvVar != nil {
			plugin.ConfigID = &p.EnvVar.ID
			plugin.Config = &types.PluginConfig{
				ID:      p.EnvVar.ID,
				OrgID:   p.OrgID,
				EnvVars: p.EnvVar.Envs,
			}
		}
		plugins = append(plugins, plugin)
	}
	return plugins, nil

	// if len(itemList) == 0 {
	// 	return nil, nil
	// }
	// pluginMap := map[string]*types.Plugin{}
	// for _, pl := range itemList {
	// 	if targetPl, ok := pluginMap[pl.Plugin.Name]; ok {
	// 		targetPl.Connections = append(targetPl.Connections, &types.PluginConnection{
	// 			ID:           pl.ID,
	// 			ConnectionID: pl.ConnectionID,
	// 			Name:         pl.Connection.Name,
	// 			Config:       pl.ConnectionConfig,
	// 		})
	// 		continue
	// 	}
	// 	pluginMap[pl.Plugin.Name] = &types.Plugin{
	// 		ID:    pl.ID,
	// 		OrgID: pl.OrgID,
	// 		Name:  pl.Plugin.Name,
	// 		Connections: []*types.PluginConnection{{
	// 			ID:           pl.ID,
	// 			ConnectionID: pl.ConnectionID,
	// 			Name:         pl.Connection.Name,
	// 			Config:       pl.ConnectionConfig,
	// 		}},
	// 		ConnectionsIDs: []string{},
	// 		Config: &types.PluginConfig{
	// 			ID:      pl.PluginID,
	// 			OrgID:   pl.OrgID,
	// 			EnvVars: pl.EnvVar.Envs,
	// 		},
	// 		ConfigID: &pl.EnvVar.ID,
	// 		Source:   pl.Plugin.Source,
	// 		// Source:        &pl.Plugin.Source,
	// 		Priority:      0,
	// 		InstalledById: "",
	// 	}
	// }
	// var pluginList []types.Plugin
	// for _, plugin := range pluginMap {
	// 	pluginList = append(pluginList, *plugin)
	// }
	// return pluginList, nil

	// ---
	// ---
	// var plugins types.Plugin

	// var plConnections []*types.PluginConnection
	// for _, pc := range itemList {
	// 	plConnections = append(plConnections, &types.PluginConnection{
	// 		ID:           pc.ID,
	// 		ConnectionID: pc.ConnectionID,
	// 		Name:         pc.Connection.Name,
	// 		Config:       pc.ConnectionConfig,
	// 	})
	// }

	// return &types.Plugin{
	// 	ID:             itemList[0].PluginID,
	// 	OrgID:          itemList[0].OrgID,
	// 	Name:           itemList[0].Plugin.Name,
	// 	Connections:    plConnections,
	// 	ConnectionsIDs: []string{},
	// 	Config: &types.PluginConfig{
	// 		ID:      itemList[0].EnvVar.ID,
	// 		OrgID:   itemList[0].OrgID,
	// 		EnvVars: itemList[0].EnvVar.Envs},
	// 	ConfigID:      &itemList[0].EnvVar.ID,
	// 	Source:        &itemList[0].Plugin.Source,
	// 	Priority:      0,
	// 	InstalledById: "",
	// }, nil
	// payload := fmt.Sprintf(`{:query {
	// 	:find [(pull ?p
	// 		[:xt/id
	//         :plugin/org
	//         :plugin/name
	//         :plugin/source
	//         :plugin/priority
	//         :plugin/installed-by
	//         :plugin/config-id
	//         :plugin/connection-ids
	//         {(:plugin/config-id {:as :plugin/config}) [:xt/id :pluginconfig/envvars]}
	//         {(:plugin/connection-ids {:as :plugin/connections})
	// 								[:xt/id
	// 								 :plugin-connection/id
	//                                  :plugin-connection/name
	//                                  :plugin-connection/config
	// 								 {(:plugin-connection/id {:as :connection}) [:connection/name]}]}])]
	// 	:in [orgid]
	// 	:where [[?p :plugin/org orgid]]}
	// 	:in-args [%q]}`, ctx.OrgID)
	// data, err := ctx.Query(payload)
	// if err != nil {
	// 	return nil, err
	// }
	// var plugins [][]types.Plugin
	// if err := edn.Unmarshal(data, &plugins); err != nil {
	// 	return nil, err
	// }

	// var itemList []types.Plugin
	// for _, p := range plugins {
	// 	// filter evicted connections that returns as nil
	// 	var pluginConnectionList []*types.PluginConnection
	// 	for _, c := range p[0].Connections {
	// 		if c == nil {
	// 			continue
	// 		}
	// 		// fixes plugin-connection/name attribute
	// 		// that is not enforced properly
	// 		c.SetName()
	// 		pluginConnectionList = append(pluginConnectionList, c)
	// 	}
	// 	p[0].Connections = pluginConnectionList
	// 	// return empty list instead of null
	// 	if p[0].Connections == nil {
	// 		p[0].Connections = []*types.PluginConnection{}
	// 	}
	// 	itemList = append(itemList, p[0])
	// }

	// return itemList, nil
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
