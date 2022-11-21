package connection

import (
	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/plugin"
	"github.com/runopsio/hoop/gateway/transport/plugins/dlp"
	"github.com/runopsio/hoop/gateway/user"
)

type (
	Service struct {
		Storage       storage
		PluginService pluginService
	}

	storage interface {
		Persist(context *user.Context, c *Connection) (int64, error)
		FindAll(context *user.Context) ([]BaseConnection, error)
		FindOne(context *user.Context, name string) (*Connection, error)
	}

	pluginService interface {
		FindOne(context *user.Context, name string) (*plugin.Plugin, error)
		Persist(context *user.Context, plugin *plugin.Plugin) error
	}

	BaseConnection struct {
		Id             string         `json:"id"       edn:"xt/id"`
		Name           string         `json:"name"     edn:"connection/name"    binding:"required"`
		Command        []string       `json:"command"  edn:"connection/command" binding:"required"`
		Type           Type           `json:"type"     edn:"connection/type"    binding:"required"`
		AgentId        string         `json:"agent_id" edn:"connection/agent"`
		SecretProvider SecretProvider `json:"-"        edn:"connection/secret-provider"`
	}

	Connection struct {
		BaseConnection
		Secret Secret `json:"secret" edn:"connection/secret"`
	}

	Secret map[string]any

	Type           string
	SecretProvider string
)

const (
	DBSecretProvider SecretProvider = "database"
)

func (s *Service) FindAll(context *user.Context) ([]BaseConnection, error) {
	return s.Storage.FindAll(context)
}

func (s *Service) Persist(context *user.Context, c *Connection) (int64, error) {
	if c.Id == "" {
		c.Id = uuid.NewString()
	}
	c.SecretProvider = DBSecretProvider

	result, err := s.Storage.Persist(context, c)
	if err != nil {
		return 0, err
	}

	s.bindAuditPlugin(context, c)
	s.bindDLPPlugin(context, c)

	return result, nil
}

func (s *Service) FindOne(context *user.Context, name string) (*Connection, error) {
	return s.Storage.FindOne(context, name)
}

func (s *Service) bindAuditPlugin(context *user.Context, conn *Connection) {
	p, err := s.PluginService.FindOne(context, "audit")
	if err != nil {
		return
	}

	if p != nil {
		registered := false
		for _, c := range p.Connections {
			if c.ConnectionId == conn.Id {
				registered = true
			}
		}
		if !registered {
			p.Connections = append(p.Connections, plugin.Connection{ConnectionId: conn.Id})
		}
	} else {
		p = &plugin.Plugin{
			Name:        "audit",
			Connections: []plugin.Connection{{ConnectionId: conn.Id}},
		}
	}

	if err := s.PluginService.Persist(context, p); err != nil {
		return
	}
}

func (s *Service) bindDLPPlugin(context *user.Context, conn *Connection) {
	p, err := s.PluginService.FindOne(context, dlp.Name)
	if err != nil {
		return
	}

	if p != nil {
		registered := false
		for _, c := range p.Connections {
			if c.ConnectionId == conn.Id {
				registered = true
			}
		}
		if !registered {
			p.Connections = append(p.Connections, plugin.Connection{ConnectionId: conn.Id})
		}
	} else {
		p = &plugin.Plugin{
			Name:        dlp.Name,
			Connections: []plugin.Connection{{ConnectionId: conn.Id}},
		}
	}

	if err := s.PluginService.Persist(context, p); err != nil {
		return
	}
}
