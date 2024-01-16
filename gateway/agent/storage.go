package agent

import (
	"github.com/runopsio/hoop/gateway/pgrest"
	pgagents "github.com/runopsio/hoop/gateway/pgrest/agents"
	"github.com/runopsio/hoop/gateway/user"
)

type Storage struct{}

func (s *Storage) FindAll(context *user.Context) ([]Agent, error) {
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

func (s *Storage) FindByNameOrID(ctx pgrest.OrgContext, nameOrID string) (*Agent, error) {
	a, err := pgagents.New().FetchOneByNameOrID(ctx, nameOrID)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, nil
	}
	return toAgent(a), nil
}

func (s *Storage) FindByToken(token string) (*Agent, error) {
	a, err := pgagents.New().FetchOneByToken(token)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, nil
	}
	return toAgent(a), nil
}

func (s *Storage) Persist(agent *Agent) (int64, error) {
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

func (s *Storage) Evict(ctx *user.Context, xtID string) error {
	return pgagents.New().Delete(ctx, xtID)
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
