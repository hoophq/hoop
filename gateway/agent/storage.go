package agent

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/pgrest"
	pgagents "github.com/runopsio/hoop/gateway/pgrest/agents"
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
	if pgrest.Rollout {
		res, err := pgagents.New().FindAll(context)
		if err != nil && err != pgrest.ErrNotFound {
			return nil, err
		}
		var agents []Agent
		for _, a := range res {
			agents = append(agents, *toAgent(&a))
		}
		return agents, nil
	}
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

func (s *Storage) FindByNameOrID(ctx *user.Context, nameOrID string) (*Agent, error) {
	if pgrest.Rollout {
		a, err := pgagents.New().FetchOneByNameOrID(ctx, nameOrID)
		if err != nil {
			return nil, err
		}
		if a == nil {
			return nil, nil
		}
		return toAgent(a), nil
	}
	agentID, agentName := "", nameOrID
	if _, err := uuid.Parse(nameOrID); err == nil {
		agentID = nameOrID
		agentName = ""
	}
	payload := fmt.Sprintf(`{:query
		{:find [(pull ?a [*])]
		:in [org-id agentname agentid]
		:where [[?a :agent/org org-id]
				(or [?a :agent/name agentname]
					[?a :xt/id agentid])]}
		:in-args [%q, %q, %q]}`, ctx.Org.Id, agentName, agentID)
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

func (s *Storage) FindByToken(token string) (*Agent, error) {
	if token == "" {
		return nil, nil
	}
	if pgrest.Rollout {
		a, err := pgagents.New().FetchOneByToken(token)
		if err != nil {
			return nil, err
		}
		if a == nil {
			return nil, nil
		}
		return toAgent(a), nil
	}

	payload := fmt.Sprintf(`{:query {
		:find [(pull ?agent [*])] 
		:in [token]
		:where [[?agent :agent/token token]]}
        :in-args [%q]}`, token)
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
	if pgrest.Rollout {
		return 0, pgagents.New().Upsert(&pgrest.Agent{
			ID:     agent.Id,
			OrgID:  agent.OrgId,
			Token:  agent.Token,
			Name:   agent.Name,
			Mode:   agent.Mode,
			Status: string(agent.Status),
			Metadata: map[string]string{
				"hostname":       agent.Hostname,
				"platform":       agent.Platform,
				"goversion":      agent.GoVersion,
				"version":        agent.Version,
				"kernel_version": agent.KernelVersion,
				"compiler":       agent.Compiler,
				"machine_id":     agent.MachineId,
			},
		})
	}
	agentPayload := st.EntityToMap(agent)

	txId, err := s.PersistEntities([]map[string]any{agentPayload})
	if err != nil {
		return 0, err
	}

	return txId, nil
}

func (s *Storage) Evict(ctx *user.Context, xtID string) error {
	if pgrest.Rollout {
		return pgagents.New().Delete(ctx, xtID)
	}
	_, err := s.Storage.SubmitEvictTx(xtID)
	return err
}

func toAgent(a *pgrest.Agent) *Agent {
	return &Agent{
		Id:            a.ID,
		Token:         a.Token,
		OrgId:         a.OrgID,
		Name:          a.Name,
		Mode:          a.Mode,
		Hostname:      a.GetMeta("hostname"),
		MachineId:     a.GetMeta("machine_id"),
		KernelVersion: a.GetMeta("kernel_version"),
		Version:       a.GetMeta("version"),
		GoVersion:     a.GetMeta("goversion"),
		Compiler:      a.GetMeta("compiler"),
		Platform:      a.GetMeta("platform"),
		Status:        Status(a.Status)}
}
