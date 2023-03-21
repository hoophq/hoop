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
		AgentId        string         `json:"agent_id" edn:"connection/agent"   binding:"required"`
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

type (
	Exec struct {
		Metadata map[string]any
		EnvVars  map[string]string
		Script   []byte
	}
	ExecRequest struct {
		Script   string `json:"script"`
		Redirect bool   `json:"redirect"`
	}
	ExecResponse struct {
		Err      error
		ExitCode int
	}
	ExecErrResponse struct {
		Message   string  `json:"message"`
		ExitCode  *int    `json:"exit_code"`
		SessionID *string `json:"session_id"`
	}
)

const (
	DBSecretProvider SecretProvider = "database"

	nilExitCode = -100
)

func (s *Service) FindAll(context *user.Context) ([]BaseConnection, error) {
	result, err := s.Storage.FindAll(context)
	if err != nil {
		return nil, err
	}

	p, err := s.PluginService.FindOne(context, pluginsrbac.Name)
	if err != nil {
		return nil, err
	}

	if p == nil || context.User.IsAdmin() {
		return result, nil
	}

	allowedConnections := make([]BaseConnection, 0)
	for _, bc := range result {
		pluginEnabled := false
		pluginAllowed := false
		for _, c := range p.Connections {
			if c.Name == bc.Name {
				pluginEnabled = true
			}
			for _, ug := range context.User.Groups {
				if pb.IsInList(ug, c.Config) {
					pluginAllowed = true
				}
			}
		}
		if !pluginEnabled || pluginAllowed {
			allowedConnections = append(allowedConnections, bc)
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
