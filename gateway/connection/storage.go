package connection

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	st "github.com/runopsio/hoop/gateway/storage"
	"github.com/runopsio/hoop/gateway/user"
	"olympos.io/encoding/edn"
)

type (
	Storage struct {
		*st.Storage
	}
)

func (s *Storage) Persist(context *user.Context, c *Connection) (int64, error) {
	secretId := uuid.New().String()

	conn := Xtdb{
		Id:             c.Id,
		OrgId:          context.Org.Id,
		Name:           c.Name,
		Command:        c.Command,
		Type:           c.Type,
		SecretProvider: c.SecretProvider,
		SecretId:       secretId,
		CreatedById:    context.User.Id,
		AgentId:        c.AgentId,
	}

	connectionPayload := st.EntityToMap(&conn)
	secretPayload := buildSecretMap(c.Secret, secretId)

	entities := []map[string]interface{}{secretPayload, connectionPayload}
	txId, err := s.PersistEntities(entities)
	if err != nil {
		return 0, err
	}

	return txId, nil
}

func (s *Storage) FindAll(context *user.Context) ([]BaseConnection, error) {
	var payload = `{:query {
		:find [(pull ?connection [*])] 
		:where [[?connection :connection/org "` +
		context.Org.Id + `"]]}}`

	b, err := s.Query([]byte(payload))
	if err != nil {
		return nil, err
	}

	var connections []BaseConnection
	if err := edn.Unmarshal(b, &connections); err != nil {
		return nil, err
	}

	return connections, nil
}

func (s *Storage) FindOne(context *user.Context, name string) (*Connection, error) {
	var payload = `{:query {
		:find [(pull ?connection [*])] 
		:where [[?connection :connection/name "` + name + `"]
                [?connection :connection/org "` + context.Org.Id + `"]]}}`

	b, err := s.Query([]byte(payload))
	if err != nil {
		return nil, err
	}

	var connections []Xtdb
	if err := edn.Unmarshal(b, &connections); err != nil {
		return nil, err
	}

	if len(connections) == 0 {
		return nil, nil
	}

	conn := connections[0]
	secret, err := s.getSecret(conn.SecretId)
	if err != nil {
		return nil, err
	}

	return &Connection{
		BaseConnection: BaseConnection{
			Id:             conn.Id,
			Name:           conn.Name,
			Command:        conn.Command,
			Type:           conn.Type,
			SecretProvider: conn.SecretProvider,
			AgentId:        conn.AgentId,
		},
		Secret: secret,
	}, nil
}

func (s *Storage) getSecret(secretId string) (Secret, error) {
	var payload = `{:query {
		:find [(pull ?secret [*])]
		:where [[?secret :xt/id "` + secretId + `"]]}}`

	b, err := s.QueryAsJson([]byte(payload))
	if err != nil {
		return nil, err
	}

	var secrets []Secret
	if err := json.Unmarshal(b, &secrets); err != nil {
		return nil, err
	}

	if len(secrets) == 0 {
		return make(map[string]interface{}), nil
	}

	return secrets[0], nil
}

func buildSecretMap(secrets map[string]interface{}, xtId string) map[string]interface{} {
	secretPayload := map[string]interface{}{
		"xt/id": xtId,
	}

	for key, value := range secrets {
		secretPayload[fmt.Sprintf("secret/%s", key)] = value
	}

	return secretPayload
}
