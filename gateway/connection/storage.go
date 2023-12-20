package connection

import (
	"errors"
	"fmt"

	"github.com/runopsio/hoop/gateway/pgrest"
	pgconnections "github.com/runopsio/hoop/gateway/pgrest/connections"
	"github.com/runopsio/hoop/gateway/user"
)

type Storage struct{}

var errNotFound = errors.New("not found")

func (s *Storage) Persist(context *user.Context, c *Connection) (int64, error) {
	envs := map[string]string{}
	for key, val := range c.Secret {
		envs[key] = fmt.Sprintf("%v", val)
	}
	return 0, pgconnections.New().Upsert(context, pgrest.Connection{
		ID:            c.Id,
		OrgID:         context.Org.Id,
		AgentID:       c.AgentId,
		LegacyAgentID: c.AgentId,
		Name:          c.Name,
		Command:       c.Command,
		Type:          string(c.Type),
		SubType:       c.Subtype,
		Envs:          envs,
	})
}

func (s *Storage) Evict(ctx *user.Context, connectionName string) error {
	return pgconnections.New().Delete(ctx, connectionName)
}

func (s *Storage) FindAll(context *user.Context) ([]BaseConnection, error) {
	items, err := pgconnections.New().FetchAll(context)
	if err != nil {
		return nil, err
	}
	var connections []BaseConnection
	for _, c := range items {
		if c.LegacyAgentID != "" {
			c.AgentID = c.LegacyAgentID
		}
		connections = append(connections, BaseConnection{
			c.ID, c.Name, "", c.Command, Type(c.Type), c.SubType, c.AgentID, DBSecretProvider,
		})
	}
	return connections, nil
}

func (s *Storage) FindOne(context *user.Context, nameOrID string) (*Connection, error) {
	conn, err := pgconnections.New().FetchOneByNameOrID(context, nameOrID)
	if err != nil {
		return nil, err
	}
	if conn == nil {
		return nil, nil
	}
	secrets := Secret{}
	for key, val := range conn.Envs {
		secrets[key] = val
	}
	return &Connection{
		BaseConnection: BaseConnection{
			Id:             conn.ID,
			Name:           conn.Name,
			IconName:       "",
			Command:        conn.Command,
			Type:           Type(conn.Type),
			Subtype:        conn.SubType,
			AgentId:        conn.AgentID,
			SecretProvider: DBSecretProvider},
		Secret: secrets,
	}, nil
}
