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
		:in [org]
		:where [[?agent :agent/org org]]}
		:in-args ["` + context.Org.Id + `"]}`

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

func (s *Storage) FindById(id string) (*Agent, error) {
	maybeAgent, err := s.GetEntity(id)
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

func (s *Storage) FindByToken(token string) (*Agent, error) {
	var payload = `{:query {
		:find [(pull ?agent [*])] 
		:in [token]
		:where [[?agent :agent/token token]]}
        :in-args ["` + token + `"]}`

	b, err := s.Query([]byte(payload))
	if err != nil {
		return nil, err
	}

	var agents []*Agent
	if err := edn.Unmarshal(b, &agents); err != nil {
		return nil, err
	}

	if len(agents) == 0 {
		return nil, nil
	}

	return agents[0], nil
}

func (s *Storage) Persist(agent *Agent) (int64, error) {
	agentPayload := st.EntityToMap(agent)

	txId, err := s.PersistEntities([]map[string]any{agentPayload})
	if err != nil {
		return 0, err
	}

	return txId, nil
}
