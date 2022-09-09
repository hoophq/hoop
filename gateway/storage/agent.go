package storage

import (
	"github.com/runopsio/hoop/domain"
	"olympos.io/encoding/edn"
)

func (s *Storage) PersistAgent(context *domain.Context, agent *domain.Agent) (int64, error) {
	agentPayload := entityToMap(agent)
	entities := []map[string]interface{}{agentPayload}
	txId, err := s.persistEntities(entities)
	if err != nil {
		return 0, err
	}

	return txId, nil
}

func (s *Storage) GetAgent(context *domain.Context, token string) (*domain.Agent, error) {
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

	return nil, nil
}
