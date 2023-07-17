package pluginstorage

import (
	"fmt"

	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"olympos.io/encoding/edn"
)

func GetByName(ctx *storagev2.Context, name string) (*types.Plugin, error) {
	payload := fmt.Sprintf(`{:query {
		:find [(pull ?p
			[:xt/id
            :plugin/org
            :plugin/name
            :plugin/source
            :plugin/priority
            :plugin/installed-by
            (:plugin/config-id {:as :plugin/config})
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
            (:plugin/config-id {:as :plugin/config})
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
