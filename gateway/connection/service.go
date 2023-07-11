package connection

import (
	"strings"

	"github.com/google/uuid"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/plugin"
	pluginsrbac "github.com/runopsio/hoop/gateway/transport/plugins/accesscontrol"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
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
		Evict(ctx *user.Context, connectionName string) error
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
		// it should deny by default.
		result, ok := mappedConfig[conn.Name]
		if !ok {
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
		// This automatically sets plugins we
		// want to be active out of the box
		// when the connection is created
		basicPlugins := []string{
			plugintypes.PluginEditorName,
			plugintypes.PluginAuditName,
			plugintypes.PluginIndexName,
			plugintypes.PluginSlackName,
		}
		pluginsWithConfig := []string{
			plugintypes.PluginDLPName,
			plugintypes.PluginRunbooksName,
		}

		// plugins that are simply configured by
		// the connection name can be passed here
		for _, plugin := range basicPlugins {
			s.bindBasicPlugin(context, c, plugin)
		}
		// plugins that need to have a
		// config field are binded here
		for _, plugin := range pluginsWithConfig {
			s.bindPluginWithConfig(context, c, plugin)
		}
	}
	return result, nil
}

func (s *Service) Evict(ctx *user.Context, connectionName string) error {
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
		}
	}

	return nil, nil
}

func (s *Service) bindBasicPlugin(context *user.Context, conn *Connection, pluginName string) {
	p, err := s.PluginService.FindOne(context, pluginName)
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
			Name:        pluginName,
			Connections: []plugin.Connection{{ConnectionId: conn.Id}},
		}
	}

	if err := s.PluginService.Persist(context, p); err != nil {
		return
	}
}

func (s *Service) bindPluginWithConfig(context *user.Context, conn *Connection, pluginName string) {
	p, err := s.PluginService.FindOne(context, pluginName)
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
			Name:        pluginName,
			Connections: []plugin.Connection{{ConnectionId: conn.Id, Config: pb.DefaultInfoTypes}},
		}
	}

	if err := s.PluginService.Persist(context, p); err != nil {
		return
	}
}
