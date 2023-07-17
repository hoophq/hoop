package plugin

import (
	"fmt"

	st "github.com/runopsio/hoop/gateway/storage"
	"github.com/runopsio/hoop/gateway/storagev2"
	pluginstorage "github.com/runopsio/hoop/gateway/storagev2/plugin"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"github.com/runopsio/hoop/gateway/user"
	"olympos.io/encoding/edn"
)

type (
	Storage struct {
		*st.Storage
	}

	xtdbConnectionData struct {
		Id   string `edn:"xt/id"`
		Name string `edn:"connection/name"`
	}
)

func (s *Storage) FindConnections(ctx *user.Context, connectionNames []string) (map[string]string, error) {
	var ednColBinding string
	for _, name := range connectionNames {
		ednColBinding += fmt.Sprintf("%q ", name)
	}
	var payload = fmt.Sprintf(`{:query {
		:find [(pull ?c [:xt/id :connection/name])]
		:in [orgid [connections ...]]
		:where [[?c :connection/org orgid]
				[?c :connection/name connections]]}
		:in-args [%q [%v]]}`, ctx.Org.Id, ednColBinding)
	b, err := s.QueryRaw([]byte(payload))
	if err != nil {
		return nil, err
	}
	var connections [][]xtdbConnectionData
	if err := edn.Unmarshal(b, &connections); err != nil {
		return nil, err
	}
	connectionMap := map[string]string{}
	if len(connections) > 0 {
		for _, connTuple := range connections {
			for _, conn := range connTuple {
				connectionMap[conn.Name] = conn.Id
			}
		}
	}
	return connectionMap, nil
}

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

func (s *Storage) PersistConfig(pconfig *PluginConfig) error {
	if _, err := s.SubmitPutTx(pconfig); err != nil {
		return fmt.Errorf("failed submiting transaction, err=%v", err)
	}
	return nil
}

func (s *Storage) FindOne(context *user.Context, name string) (*types.Plugin, error) {
	ctxv2 := storagev2.NewContext(context.User.Id, context.Org.Id, storagev2.NewStorage(nil))
	return pluginstorage.GetByName(ctxv2, name)
}
