package agent

import (
	st "github.com/runopsio/hoop/gateway/storage"
	"github.com/runopsio/hoop/gateway/user"
	"olympos.io/encoding/edn"
)

type (
	Storage struct {
		st.Storage
	}
)

func (s *Storage) FindAll(context user.Context) ([]Agent, error) {
	var payload = `{:query {
		:find [(pull ?Agent [*])] 
		:where [[?Agent :Agent/org "` +
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

func (s *Storage) GetByToken(token string) (*Agent, error) {
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
