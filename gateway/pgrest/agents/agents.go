package pgagents

import (
	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/pgrest"
)

type agent struct{}

func New() *agent { return &agent{} }

// FindAll returns all agents from all organization if the context is empty.
// Otherwise return all the agents from a specific organization.
func (a *agent) FindAll(ctx pgrest.OrgContext) ([]pgrest.Agent, error) {
	orgID := ctx.GetOrgID()
	client := pgrest.New("/agents")
	if orgID != "" {
		client = pgrest.New("/agents?org_id=eq.%v", orgID)
	}
	var res []pgrest.Agent
	if err := client.List().DecodeInto(&res); err != nil && err != pgrest.ErrNotFound {
		return nil, err
	}
	return res, nil
}

func (a *agent) FetchOneByNameOrID(ctx pgrest.OrgContext, nameOrID string) (*pgrest.Agent, error) {
	client := pgrest.New("/agents?org_id=eq.%v&name=eq.%v", ctx.GetOrgID(), nameOrID)
	if _, err := uuid.Parse(nameOrID); err == nil {
		client = pgrest.New("/agents?org_id=eq.%v&id=eq.%v", ctx.GetOrgID(), nameOrID)
	}
	var agent pgrest.Agent
	if err := client.FetchOne().DecodeInto(&agent); err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &agent, nil
}

func (a *agent) FetchOneByToken(token string) (*pgrest.Agent, error) {
	var agent pgrest.Agent
	if err := pgrest.New("/agents?token=eq.%v", token).
		FetchOne().
		DecodeInto(&agent); err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &agent, nil
}

func (a *agent) Upsert(agent *pgrest.Agent) error {
	status := "DISCONNECTED"
	if agent.Status != "" {
		status = agent.Status
	}
	return pgrest.New("/agents").Upsert(map[string]any{
		"id":         agent.ID,
		"token":      agent.Token,
		"org_id":     agent.OrgID,
		"name":       agent.Name,
		"mode":       agent.Mode,
		"status":     status,
		"updated_at": agent.UpdatedAt,
		"metadata": map[string]string{
			"hostname":       agent.GetMeta("hostname"),
			"platform":       agent.GetMeta("platform"),
			"goversion":      agent.GetMeta("goversion"),
			"version":        agent.GetMeta("version"),
			"kernel_version": agent.GetMeta("kernel_version"),
			"compiler":       agent.GetMeta("compiler"),
			"machine_id":     agent.GetMeta("machine_id"),
		},
	}).Error()
}

func (a *agent) Delete(ctx pgrest.OrgContext, id string) error {
	return pgrest.New("/agents?org_id=eq.%s&id=eq.%v", ctx.GetOrgID(), id).Delete().Error()
}
