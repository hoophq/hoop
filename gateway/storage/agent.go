package storage

import (
	"github.com/runopsio/hoop/gateway/domain"
	"olympos.io/encoding/edn"
)

func (s *Storage) PersistAgent(agent *domain.Agent) (int64, error) {
	agentPayload := entityToMap(agent)
	entities := []map[string]interface{}{agentPayload}
	txId, err := s.persistEntities(entities)
	if err != nil {
		return 0, err
	}

	return txId, nil
}

func (s *Storage) GetAgents(context *domain.Context) ([]domain.Agent, error) {
	var payload = `{:query {
		:find [(pull ?agent [*])] 
		:where [[?agent :agent/org "` +
		context.Org.Id + `"]]}}`

	b, err := s.query([]byte(payload))
	if err != nil {
		return nil, err
	}

	var agents []domain.Agent
	if err := edn.Unmarshal(b, &agents); err != nil {
		return nil, err
	}

	return agents, nil
}
