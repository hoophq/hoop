package plugin

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/user"
)

type (
	Service struct {
		Storage storage
	}

	storage interface {
		PersistConfig(*PluginConfig) error
		Persist(context *user.Context, plugin *Plugin) (int64, error)
		FindAll(context *user.Context) ([]ListPlugin, error)
		FindOne(context *user.Context, name string) (*Plugin, error)
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

func (s *Service) FindOne(context *user.Context, name string) (*Plugin, error) {
	return s.Storage.FindOne(context, name)
}

func (s *Service) FindAll(context *user.Context) ([]ListPlugin, error) {
	return s.Storage.FindAll(context)
}

func (s *Service) Persist(context *user.Context, plugin *Plugin) error {
	if plugin.Id == "" {
		plugin.Id = uuid.NewString()
	}

	connections := plugin.Connections
	connectionIDs := make([]string, 0)
	connConfigs := make([]Connection, 0)

	for i, c := range plugin.Connections {
		if c.ConnectionId == "" {
			return errors.New("missing connection ID")
		}
		connConfigID := uuid.NewString()
		connections[i].Id = connConfigID
		connectionIDs = append(connectionIDs, connConfigID)
		connConfigs = append(connConfigs, plugin.Connections[i])
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
