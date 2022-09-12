package agent

import (
	st "github.com/runopsio/hoop/gateway/storage"
	"github.com/runopsio/hoop/gateway/user"
	"olympos.io/encoding/edn"
)

type (
	Storage struct {
		*st.Storage
	}
)

func (s *Storage) FindAll(context *user.Context) ([]Agent, error) {
	var payload = `{:query {
		:find [(pull ?agent [*])] 
		:where [[?agent :agent/org "` +
		context.Org.Id + `"]]}}`

	b, err := s.Query([]byte(payload))
	if err != nil {
		return nil, err
	}

	var agents []Agent
	if err := edn.Unmarshal(b, &agents); err != nil {
		return nil, err
	}

	return agents, nil
}

func (s *Storage) FindOne(token string) (*Agent, error) {
	maybeAgent, err := s.GetEntity(token)
	if err != nil {
		return nil, err
	}

	if maybeAgent == nil {
		return nil, nil
	}

	var agent Agent
	if err := edn.Unmarshal(maybeAgent, &agent); err != nil {
		return nil, err
	}

	return &agent, nil
}

func (s *Storage) Persist(agent *Agent) (int64, error) {
	agentPayload := st.EntityToMap(agent)

	txId, err := s.PersistEntities([]map[string]interface{}{agentPayload})
	if err != nil {
		return 0, err
	}

	return txId, nil
}
