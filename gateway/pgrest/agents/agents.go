package pgagents

import (
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/pgrest"
)

type agent struct{}

func New() *agent { return &agent{} }

// FindAll returns all agents from all organization if the context is empty.
// Otherwise return all the agents from a specific organization.
func (a *agent) FindAll(ctx pgrest.OrgContext) ([]pgrest.Agent, error) {
	var res []pgrest.Agent
	if err := pgrest.New("/agents?org_id=eq.%v&order=name.asc", ctx.GetOrgID()).
		List().
		DecodeInto(&res); err != nil && err != pgrest.ErrNotFound {
		return nil, err
	}
	return res, nil
}

func (a *agent) FetchOneByNameOrID(ctx pgrest.OrgContext, nameOrID string) (*pgrest.Agent, error) {
	client := pgrest.New("/agents?org_id=eq.%v&name=eq.%v", ctx.GetOrgID(), url.QueryEscape(nameOrID))
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
	if err := pgrest.New("/agents?select=*,orgs(name,license)&key_hash=eq.%v", url.QueryEscape(token)).
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
	status := pgrest.AgentStatusDisconnected
	if agent.Status != "" {
		status = agent.Status
	}
	return pgrest.New("/agents").Upsert(map[string]any{
		"id":         agent.ID,
		"key_hash":   agent.KeyHash,
		"key":        agent.Key,
		"org_id":     agent.OrgID,
		"name":       agent.Name,
		"mode":       agent.Mode,
		"status":     status,
		"updated_at": agent.UpdatedAt,
		"metadata":   agent.Metadata,
	}).Error()
}

func (a *agent) UpdateStatus(ctx pgrest.OrgContext, agentID, status string, metadata map[string]string) error {
	patchBody := map[string]any{
		"status":     status,
		"updated_at": time.Now().UTC(),
	}
	if len(metadata) > 0 {
		patchBody["metadata"] = metadata
	}
	err := pgrest.New("/agents?org_id=eq.%v&id=eq.%v", ctx.GetOrgID(), agentID).
		Patch(patchBody).
		Error()
	if err == pgrest.ErrNotFound {
		return nil
	}
	return err
}

// UpdateAllToOffline update the status of all agent resources to offline
func (a *agent) UpdateAllToOffline() error {
	err := pgrest.New("/agents").Patch(map[string]any{
		"status":     pgrest.AgentStatusDisconnected,
		"updated_at": time.Now().UTC(),
	}).Error()
	if err == pgrest.ErrNotFound {
		return nil
	}
	return err
}

func (a *agent) Delete(ctx pgrest.OrgContext, id string) error {
	return pgrest.New("/agents?org_id=eq.%s&id=eq.%v", ctx.GetOrgID(), url.QueryEscape(id)).Delete().Error()
}
