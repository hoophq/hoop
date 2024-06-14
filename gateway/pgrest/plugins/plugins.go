package pgplugins

import (
	"fmt"
	"net/url"

	"github.com/runopsio/hoop/gateway/pgrest"
	"github.com/runopsio/hoop/gateway/storagev2/types"
)

type plugins struct{}

func New() *plugins { return &plugins{} }

func (p *plugins) Upsert(ctx pgrest.OrgContext, existentPlugin, pl *types.Plugin) (err error) {
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
		return fmt.Errorf("plugin_connections: %v", err)
	}
	// best effort, delete all non-enabled plugin connections
	_ = pgrest.New("/plugin_connections?org_id=eq.%s&enabled=is.false", pl.OrgID).Delete()
	return nil
}

func (p *plugins) FetchOne(ctx pgrest.OrgContext, name string) (*types.Plugin, error) {
	name = url.QueryEscape(name)
	var pl pgrest.Plugin
	if err := pgrest.New("/plugins?select=*,env_vars(id,envs)&org_id=eq.%v&name=eq.%s", ctx.GetOrgID(), name).
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
		ctx.GetOrgID(), name).
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
}

func (p *plugins) FetchAll(ctx pgrest.OrgContext) ([]types.Plugin, error) {
	var pgplugins []pgrest.Plugin
	err := pgrest.New("/plugins?select=*,env_vars(id,envs)&org_id=eq.%v", ctx.GetOrgID()).
		List().
		DecodeInto(&pgplugins)
	if err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}

	var pgPlConnections []pgrest.PluginConnection
	err = pgrest.New("/plugin_connections?select=*,connections(id,name),env_vars(id,envs),plugins(id,name)&org_id=eq.%s&enabled=is.true)", ctx.GetOrgID()).
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
}
