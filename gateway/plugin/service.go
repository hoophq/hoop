package plugin

import (
	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/user"
)

type (
	Service struct {
		Storage storage
	}

	storage interface {
		Persist(context *user.Context, plugin *Plugin, connConfigs []Connection) (int64, error)
		FindAll(context *user.Context) ([]ListPlugin, error)
		FindOne(context *user.Context, name string) (*Plugin, error)
	}

	Plugin struct {
		Id             string       `json:"id"          edn:"xt/id"`
		OrgId          string       `json:"-"           edn:"plugin/org"`
		Name           string       `json:"name"        edn:"plugin/name"          binding:"required"`
		Connections    []Connection `json:"connections" edn:"plugin/connections"   binding:"required"`
		ConnectionsIDs []string     `json:"-"           edn:"plugin/connection-ids"`
		InstalledById  string       `json:"-"           edn:"plugin/installed-by"`
	}

	Connection struct {
		Id           string              `json:"-"       edn:"xt/id"`
		ConnectionId string              `json:"id"      edn:"plugin-connection/id"      binding:"required"`
		Name         string              `json:"name"    edn:"plugin-connection/name"`
		Config       []string            `json:"config"  edn:"plugin-connection/config"  binding:"required"`
		Groups       map[string][]string `json:"groups"  edn:"plugin-connection/groups"`
	}

	ListPlugin struct {
		Plugin
		Connections []string `json:"connections" edn:"plugin/connection-ids"`
	}

	xtdbList struct {
		Plugin
		Connections []xtdbConnection `edn:"plugin/connection-ids"`
	}

	xtdbConnection struct {
		Conn xtdbConnectionName `edn:"plugin-connection/id"`
	}

	xtdbConnectionName struct {
		Name string `edn:"connection/name"`
	}
)

func (s *Service) FindAll(context *user.Context) ([]ListPlugin, error) {
	return s.Storage.FindAll(context)
}

func (s *Service) Persist(context *user.Context, plugin *Plugin) (int64, error) {
	connIDs := make([]string, 0)
	connConfigs := make([]Connection, 0)
	connections := plugin.Connections

	for i := range plugin.Connections {
		connConfigID := uuid.NewString()
		connections[i].Id = connConfigID
		connIDs = append(connIDs, connConfigID)
		connConfigs = append(connConfigs, plugin.Connections[i])
	}

	plugin.Id = uuid.NewString()
	plugin.ConnectionsIDs = connIDs
	plugin.Connections = nil

	tx, err := s.Storage.Persist(context, plugin, connConfigs)
	if err != nil {
		return 0, err
	}

	plugin.Connections = connections
	return tx, nil
}

func (s *Service) FindOne(context *user.Context, name string) (*Plugin, error) {
	return s.Storage.FindOne(context, name)
}
