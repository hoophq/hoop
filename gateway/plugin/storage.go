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
)

func (s *Storage) Persist(context *user.Context, plugin *Plugin, connConfigs []Connection) (int64, error) {
	plugin.OrgId = context.Org.Id
	plugin.InstalledById = context.User.Id

	payloads := make([]map[string]interface{}, 0)
	payloads = append(payloads, st.EntityToMap(plugin))

	for _, c := range connConfigs {
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
		:where [[?plugin :plugin/org "` +
		context.Org.Id + `"]]}}`

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
				Name: p.Name,
			},
			Connections: connections,
		})
	}

	return plugins, nil
}

func (s *Storage) FindOne(context *user.Context, name string) (*Plugin, error) {
	var payload = `{:query {
		:find [(pull ?plugin [* {:plugin/connection-ids [*]}])] 
		:where [[?plugin :plugin/name "` + name + `"]
                [?plugin :plugin/org "` + context.Org.Id + `"]]}}`

	b, err := s.Query([]byte(payload))
	if err != nil {
		return nil, err
	}

	b = sanitizeEdnGroups(b)

	var plugins []Plugin
	if err := edn.Unmarshal(b, &plugins); err != nil {
		return nil, err
	}

	if len(plugins) == 0 {
		return nil, nil
	}

	return &plugins[0], nil
}

func sanitizeEdnGroups(b []byte) []byte {
	return b
}
