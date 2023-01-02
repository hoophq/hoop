package plugin

import (
	st "github.com/runopsio/hoop/gateway/storage"
	"github.com/runopsio/hoop/gateway/user"
	"olympos.io/encoding/edn"
)

type (
	Storage struct {
		*st.Storage
	}

	xtdbList struct {
		Plugin
		Connections []xtdbListConnection `edn:"plugin/connection-ids"`
	}

	xtdbListConnection struct {
		Conn xtdbListConnectionName `edn:"plugin-connection/id"`
	}

	xtdbListConnectionName struct {
		Name string `edn:"connection/name"`
	}

	xtdbPlugin struct {
		Plugin
		XtdbConnection []xtdbConnection `edn:"plugin/connection-ids"`
	}

	xtdbConnection struct {
		Connection
		ConnData xtdbConnectionData       `edn:"plugin-connection/id"`
		Groups   map[edn.Keyword][]string `edn:"plugin-connection/groups"`
	}

	xtdbConnectionData struct {
		Id   string `edn:"xt/id"`
		Name string `edn:"connection/name"`
	}
)

func (s *Storage) Persist(context *user.Context, plugin *Plugin) (int64, error) {
	plugin.OrgId = context.Org.Id
	plugin.InstalledById = context.User.Id

	payloads := make([]map[string]any, 0)
	payloads = append(payloads, st.EntityToMap(plugin))

	for _, c := range plugin.Connections {
		payloads = append(payloads, st.EntityToMap(&c))
	}

	txId, err := s.PersistEntities(payloads)
	if err != nil {
		return 0, err
	}

	return txId, nil
}

func (s *Storage) FindAll(context *user.Context) ([]ListPlugin, error) {
	var payload = `{:query {
		:find [(pull ?plugin [* {:plugin/connection-ids [{:plugin-connection/id [:connection/name]}]}])] 
		:in [org]
		:where [[?plugin :plugin/org org]]}
		:in-args ["` + context.Org.Id + `"]}`

	b, err := s.Query([]byte(payload))
	if err != nil {
		return nil, err
	}

	var xtdbPlugins []xtdbList
	if err := edn.Unmarshal(b, &xtdbPlugins); err != nil {
		return nil, err
	}

	plugins := make([]ListPlugin, 0)
	for _, p := range xtdbPlugins {
		connections := make([]string, 0)
		for _, c := range p.Connections {
			connections = append(connections, c.Conn.Name)
		}

		plugins = append(plugins, ListPlugin{
			Plugin: Plugin{
				Id:   p.Id,
				Name: p.Name,
				Type: p.Type,
			},
			Connections: connections,
		})
	}

	return plugins, nil
}

func (s *Storage) FindOne(context *user.Context, name string) (*Plugin, error) {
	var payload = `{:query {
		:find [(pull ?plugin [* {:plugin/connection-ids [* {:plugin-connection/id [*]}]}])] 
		:in [name org]
		:where [[?plugin :plugin/name name]
                [?plugin :plugin/org org]]}
		:in-args ["` + name + `" "` + context.Org.Id + `"]}`

	b, err := s.Query([]byte(payload))
	if err != nil {
		return nil, err
	}

	var plugins []xtdbPlugin
	if err := edn.Unmarshal(b, &plugins); err != nil {
		return nil, err
	}

	if len(plugins) == 0 {
		return nil, nil
	}

	xtdbPlugin := plugins[0]

	connections := make([]Connection, 0)
	for _, c := range xtdbPlugin.XtdbConnection {
		connections = append(connections, Connection{
			Id:           c.Id,
			ConnectionId: c.ConnData.Id,
			Name:         c.ConnData.Name,
			Config:       c.Config,
			Groups:       sanitizeEdnGroups(c.Groups),
		})
	}

	plugin := &Plugin{
		Id:             xtdbPlugin.Id,
		OrgId:          xtdbPlugin.OrgId,
		Name:           xtdbPlugin.Name,
		Type:           xtdbPlugin.Type,
		Connections:    connections,
		ConnectionsIDs: xtdbPlugin.ConnectionsIDs,
		InstalledById:  xtdbPlugin.InstalledById,
	}

	return plugin, nil
}

func sanitizeEdnGroups(groups map[edn.Keyword][]string) map[string][]string {
	strGroups := make(map[string][]string)
	for k, v := range groups {
		strGroups[removeKeyword(k)] = v
	}
	return strGroups
}

func removeKeyword(keyword edn.Keyword) string {
	s := keyword.String()
	return s[1:]
}
