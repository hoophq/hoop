package storage

import (
	"fmt"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/domain"
	"olympos.io/encoding/edn"
)

func (s *Storage) PersistConnection(context *domain.Context, c *domain.ConnectionOne) (int64, error) {
	connectionId := uuid.New().String()
	secretId := uuid.New().String()

	conn := domain.ConnectionWrite{
		Id:          connectionId,
		OrgId:       context.Org.Id,
		Name:        c.Name,
		Command:     c.Command,
		Type:        c.Type,
		Provider:    domain.DBSecretProvider,
		SecretId:    secretId,
		CreatedById: context.User.Id,
	}

	connectionPayload := entityToMap(&conn)
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

	//for i, c := range connections {
	//	secrets := make(map[string]interface{})
	//	for k, v := range c.Secret {
	//		secrets[strings.Split(k, "/")[1]] = v
	//	}
	//	connections[i].Secret = secrets
	//}

	return connections, nil
}

func (s *Storage) GetConnection(context *domain.Context, name string) (*domain.ConnectionOne, error) {
	return nil, nil
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
