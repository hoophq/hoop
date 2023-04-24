package connection

import (
	"strings"

	"github.com/google/uuid"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/plugin"
	pluginsrbac "github.com/runopsio/hoop/gateway/transport/plugins/accesscontrol"
	pluginsaudit "github.com/runopsio/hoop/gateway/transport/plugins/audit"
	pluginsdlp "github.com/runopsio/hoop/gateway/transport/plugins/dlp"
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
		Evict(ctx *user.Context, connectionName string) (bool, error)
	}

	pluginService interface {
		FindOne(context *user.Context, name string) (*plugin.Plugin, error)
		Persist(context *user.Context, plugin *plugin.Plugin) error
	}

	BaseConnection struct {
		Id             string         `json:"id"        edn:"xt/id"`
		Name           string         `json:"name"      edn:"connection/name"       binding:"required"`
		IconName       string         `json:"icon_name" edn:"connection/icon-name"`
		Command        []string       `json:"command"   edn:"connection/command"    binding:"required"`
		Type           Type           `json:"type"      edn:"connection/type"       binding:"required"`
		AgentId        string         `json:"agent_id"  edn:"connection/agent"      binding:"required"`
		SecretProvider SecretProvider `json:"-"         edn:"connection/secret-provider"`
	}

	Connection struct {
		BaseConnection
		Secret Secret `json:"secret" edn:"connection/secret"`
	}

	Secret map[string]any

	Type           string
	SecretProvider string
)

const DBSecretProvider SecretProvider = "database"

func (s *Service) FindAll(context *user.Context) ([]BaseConnection, error) {
	all, err := s.Storage.FindAll(context)
	if err != nil {
		return nil, err
	}

	p, err := s.PluginService.FindOne(context, pluginsrbac.Name)
	if err != nil {
		return nil, err
	}

	if p == nil || context.User.IsAdmin() {
		return all, nil
	}

	// put all the connection configured in a
	// map with its corresponding configuration
	mappedConfig := make(map[string][]string)
	for _, c := range p.Connections {
		mappedConfig[c.Name] = c.Config
	}

	allowedConnections := make([]BaseConnection, 0)
	for _, conn := range all {
		// if the plugin does not contain the connection,
		// then it is allowed to be seen by everyone
		result, ok := mappedConfig[conn.Name]
		if !ok {
			allowedConnections = append(allowedConnections, conn)
			continue
		}

		// match the registered connections against users config
		for _, g := range context.User.Groups {
			if pb.IsInList(g, result) {
				allowedConnections = append(allowedConnections, conn)
				break
			}
		}
	}

	return allowedConnections, nil
}

func (s *Service) Persist(httpMethod string, context *user.Context, c *Connection) (int64, error) {
	if c.Id == "" {
		c.Id = uuid.NewString()
	}
	c.SecretProvider = DBSecretProvider

	result, err := s.Storage.Persist(context, c)
	if err != nil {
		return 0, err
	}

	if strings.ToUpper(httpMethod) == "POST" {
		s.bindAuditPlugin(context, c)
		s.bindDLPPlugin(context, c)
	}
	return result, nil
}

func (s *Service) Evict(ctx *user.Context, connectionName string) (bool, error) {
	return s.Storage.Evict(ctx, connectionName)
}

func (s *Service) FindOne(context *user.Context, name string) (*Connection, error) {
	result, err := s.Storage.FindOne(context, name)
	if err != nil {
		return nil, err
	}

	if result == nil {
		return nil, nil
	}

	p, err := s.PluginService.FindOne(context, pluginsrbac.Name)
	if err != nil {
		return nil, err
	}

	if p == nil || context.User.IsAdmin() {
		return result, nil
	}

	for _, c := range p.Connections {
		if c.Name == name {
			for _, ug := range context.User.Groups {
				if pb.IsInList(ug, c.Config) {
					return result, nil
				}
			}
			return nil, nil
		}
	}

	return result, nil
}

func (s *Service) bindAuditPlugin(context *user.Context, conn *Connection) {
	p, err := s.PluginService.FindOne(context, pluginsaudit.Name)
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
	p, err := s.PluginService.FindOne(context, pluginsdlp.Name)
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
			p.Connections = append(p.Connections, plugin.Connection{
				ConnectionId: conn.Id,
				Config:       pb.DefaultInfoTypes,
			})
		}
	} else {
		p = &plugin.Plugin{
			Name:        pluginsdlp.Name,
			Connections: []plugin.Connection{{ConnectionId: conn.Id, Config: pb.DefaultInfoTypes}},
		}
	}

	if err := s.PluginService.Persist(context, p); err != nil {
		return
	}
}
