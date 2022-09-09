package storage

import (
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/domain"
	"olympos.io/encoding/edn"
)

func (s *Storage) PersistConnection(context *domain.Context, c *domain.Connection) (int64, error) {
	secretId := uuid.New().String()

	conn := domain.ConnectionXtdb{
		Id:          c.Id,
		OrgId:       context.Org.Id,
		Name:        c.Name,
		Command:     c.Command,
		Type:        c.Type,
		Provider:    c.Provider,
		SecretId:    secretId,
		CreatedById: context.User.Id,
	}

	connectionPayload := EntityToMap(&conn)
	secretPayload := buildSecretMap(c.Secret, secretId)

	entities := []map[string]interface{}{secretPayload, connectionPayload}
	txId, err := s.persistEntities(entities)
	if err != nil {
		return 0, err
	}

	return txId, nil
}

func (s *Storage) GetConnections(context *domain.Context) ([]domain.ConnectionList, error) {
	var payload = `{:query {
		:find [(pull ?connection [*])] 
		:where [[?connection :connection/org "` +
		context.Org.Id + `"]]}}`

	b, err := s.query([]byte(payload))
	if err != nil {
		return nil, err
	}

	var connections []domain.ConnectionList
	if err := edn.Unmarshal(b, &connections); err != nil {
		return nil, err
	}

	return connections, nil
}

func (s *Storage) GetConnection(context *domain.Context, name string) (*domain.Connection, error) {
	var payload = `{:query {
		:find [(pull ?connection [*])] 
		:where [[?connection :connection/name "` + name + `"]
                [?connection :connection/org "` + context.Org.Id + `"]]}}`

	b, err := s.query([]byte(payload))
	if err != nil {
		return nil, err
	}

	var connections []domain.ConnectionXtdb
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

	return &domain.Connection{
		ConnectionList: domain.ConnectionList{
			Id:       conn.Id,
			Name:     conn.Name,
			Command:  conn.Command,
			Type:     conn.Type,
			Provider: conn.Provider,
		},
		Secret: secret,
	}, nil
}

func (s *Storage) getSecret(secretId string) (domain.Secret, error) {
	var payload = `{:query {
		:find [(pull ?secret [*])]
		:where [[?secret :xt/id "` + secretId + `"]]}}`

	b, err := s.queryAsJson([]byte(payload))
	if err != nil {
		return nil, err
	}

	var secrets []domain.Secret
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
