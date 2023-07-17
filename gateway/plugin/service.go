package plugin

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/storagev2"
	pluginstorage "github.com/runopsio/hoop/gateway/storagev2/plugin"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"github.com/runopsio/hoop/gateway/user"
)

type (
	Service struct {
		Storage storage
	}

	storage interface {
		PersistConfig(*PluginConfig) error
		Persist(context *user.Context, plugin *Plugin) (int64, error)
		FindOne(context *user.Context, name string) (*types.Plugin, error)
		FindConnections(ctx *user.Context, connectionNames []string) (map[string]string, error)
	}

	Plugin struct {
		Id             string        `json:"id"           edn:"xt/id"`
		OrgId          string        `json:"-"            edn:"plugin/org"`
		ConfigID       *string       `json:"-"            edn:"plugin/config-id"`
		Config         *PluginConfig `json:"config"       edn:"plugin/config"`
		Source         *string       `json:"source"       edn:"plugin/source"`
		Priority       int           `json:"priority"     edn:"plugin/priority"`
		Name           string        `json:"name"         edn:"plugin/name"          binding:"required"`
		Connections    []Connection  `json:"connections"  edn:"plugin/connections"   binding:"required"`
		ConnectionsIDs []string      `json:"-"            edn:"plugin/connection-ids"`
		InstalledById  string        `json:"-"            edn:"plugin/installed-by"`
	}

	Connection struct {
		Id           string              `json:"-"       edn:"xt/id"`
		ConnectionId string              `json:"id"      edn:"plugin-connection/id"      binding:"required"`
		Name         string              `json:"name"    edn:"plugin-connection/name"`
		Config       []string            `json:"config"  edn:"plugin-connection/config"  binding:"required"`
		Groups       map[string][]string `json:"groups"  edn:"plugin-connection/groups"`
	}

	PluginConfig struct {
		ID      string            `json:"id"      edn:"xt/id"`
		Org     string            `json:"-"       edn:"pluginconfig/org"`
		EnvVars map[string]string `json:"envvars" edn:"pluginconfig/envvars"`
	}

	ListPlugin struct {
		Plugin
		Connections []string `json:"connections" edn:"plugin/connection-ids"`
	}
)

func (s *Service) FindOne(context *user.Context, name string) (*types.Plugin, error) {
	storev2 := storagev2.NewStorage(nil)
	ctxv2 := storagev2.NewOrganizationContext(context.Org.Id, storev2)
	return pluginstorage.GetByName(ctxv2, name)
}

func (s *Service) FindAll(context *user.Context) ([]types.Plugin, error) {
	storev2 := storagev2.NewStorage(nil)
	ctxv2 := storagev2.NewOrganizationContext(context.Org.Id, storev2)
	return pluginstorage.List(ctxv2)
}

func (s *Service) Persist(context *user.Context, plugin *Plugin) error {
	if plugin.Id == "" {
		plugin.Id = uuid.NewString()
	}

	connectionIDs := make([]string, 0)
	connConfigs := make([]Connection, 0)

	var connectionNames []string
	for _, c := range plugin.Connections {
		if c.Name == "" {
			connectionNames = nil
			break
		}
		connectionNames = append(connectionNames, c.Name)
	}
	var connectionMap map[string]string
	if len(connectionNames) > 0 {
		var err error
		connectionMap, err = s.Storage.FindConnections(context, connectionNames)
		if err != nil {
			return fmt.Errorf("failed looking up for existent connections %v", err)
		}
		if len(connectionMap) != len(plugin.Connections) {
			return fmt.Errorf("check if the input connections exists, found=%v/%v",
				len(connectionMap), len(plugin.Connections))
		}
	}
	if connectionMap == nil {
		connectionMap = map[string]string{}
	}

	for _, connConfig := range plugin.Connections {
		// avoids inconsistency by using the connection
		// retrieved from the storage
		if connConfig.ConnectionId == "" {
			connectionID, ok := connectionMap[connConfig.Name]
			if !ok {
				return errors.New("missing connection ID")
			}
			connConfig.ConnectionId = connectionID
		}
		connConfigID := uuid.NewString()
		connectionIDs = append(connectionIDs, connConfigID)
		connConfigs = append(connConfigs, Connection{
			Id:           connConfigID,
			ConnectionId: connConfig.ConnectionId,
			Name:         connConfig.Name,
			Config:       connConfig.Config,
			Groups:       connConfig.Groups,
		})
	}

	plugin.ConnectionsIDs = connectionIDs
	plugin.Connections = connConfigs

	_, err := s.Storage.Persist(context, plugin)
	if err != nil {
		return err
	}

	return nil
}

func (s *Service) PersistConfig(context *user.Context, pluginConfig *PluginConfig) error {
	if context.Org.Id == "" {
		return fmt.Errorf("missing org id")
	}
	pluginConfig.Org = context.Org.Id
	return s.Storage.PersistConfig(pluginConfig)
}
