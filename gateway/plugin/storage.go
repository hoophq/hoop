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

func (s *Storage) Persist(context *user.Context, plugin *Plugin) (int64, error) {
	plugin.OrgId = context.Org.Id
	plugin.InstalledById = context.User.Id
	pluginPayload := st.EntityToMap(plugin)

	txId, err := s.PersistEntities([]map[string]interface{}{pluginPayload})
	if err != nil {
		return 0, err
	}

	return txId, nil
}

func (s *Storage) FindAll(context *user.Context) ([]Plugin, error) {
	var payload = `{:query {
		:find [(pull ?plugin [*])] 
		:where [[?plugin :plugin/org "` +
		context.Org.Id + `"]]}}`

	b, err := s.Query([]byte(payload))
	if err != nil {
		return nil, err
	}

	var plugins []Plugin
	if err := edn.Unmarshal(b, &plugins); err != nil {
		return nil, err
	}

	return plugins, nil
}

func (s *Storage) FindOne(context *user.Context, name string) (*Plugin, error) {
	var payload = `{:query {
		:find [(pull ?plugin [*])] 
		:where [[?plugin :plugin/name "` + name + `"]
                [?plugin :plugin/org "` + context.Org.Id + `"]]}}`

	b, err := s.Query([]byte(payload))
	if err != nil {
		return nil, err
	}

	var plugins []Plugin
	if err := edn.Unmarshal(b, &plugins); err != nil {
		return nil, err
	}

	if len(plugins) == 0 {
		return nil, nil
	}

	return &plugins[0], nil
}
