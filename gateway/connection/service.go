package connection

import (
	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/user"
)

type (
	Service struct {
		Storage storage
	}

	storage interface {
		Persist(context *user.Context, c *Connection) (int64, error)
		FindAll(context *user.Context) ([]BaseConnection, error)
		FindOne(context *user.Context, name string) (*Connection, error)
	}

	BaseConnection struct {
		Id             string         `json:"id"       edn:"xt/id"`
		Name           string         `json:"name"     edn:"connection/name"    binding:"required"`
		Command        []string       `json:"command"  edn:"connection/command" binding:"required"`
		Type           Type           `json:"type"     edn:"connection/type"    binding:"required"`
		SecretProvider SecretProvider `json:"provider" edn:"connection/secret-provider"`
		AgentId        string         `json:"agent_id" edn:"connection/agent"`
	}

	Connection struct {
		BaseConnection
		Secret Secret `json:"secret" edn:"connection/secret"`
	}

	Secret map[string]interface{}

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
	c.Id = uuid.NewString()
	c.SecretProvider = DBSecretProvider

	return s.Storage.Persist(context, c)
}

func (s *Service) FindOne(context *user.Context, name string) (*Connection, error) {
	return s.Storage.FindOne(context, name)
}
