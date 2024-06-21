package pgconnections

import (
	"net/url"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/pgrest"
	"github.com/runopsio/hoop/gateway/storagev2/types"
)

type connections struct{}

func New() *connections { return &connections{} }

func (c *connections) FetchByIDs(ctx pgrest.OrgContext, connectionIDs []string) (map[string]types.Connection, error) {
	var connList []pgrest.Connection
	itemMap := map[string]types.Connection{}
	err := pgrest.New("/connections?org_id=eq.%s", ctx.GetOrgID()).
		List().
		DecodeInto(&connList)
	if err != nil {
		if err == pgrest.ErrNotFound {
			return itemMap, nil
		}
		return nil, err
	}
	for _, conn := range connList {
		for _, connID := range connectionIDs {
			if conn.ID == connID {
				itemMap[connID] = types.Connection{
					Id:      conn.ID,
					OrgId:   conn.OrgID,
					Name:    conn.Name,
					Command: conn.Command,
					Type:    conn.Type,
					SubType: conn.SubType,
					AgentId: conn.AgentID,
				}
				break
			}
		}
	}
	return itemMap, nil
}

func (a *connections) FetchOneByNameOrID(ctx pgrest.OrgContext, nameOrID string) (*pgrest.Connection, error) {
	client := pgrest.New("/connections?select=*,orgs(id,name),agents(id,name,mode),plugin_connections(config,plugins(name))&org_id=eq.%s&name=eq.%s",
		ctx.GetOrgID(), url.QueryEscape(nameOrID))
	if _, err := uuid.Parse(nameOrID); err == nil {
		client = pgrest.New("/connections?select=*,orgs(id,name),agents(id,name,mode),plugin_connections(config,plugins(name))&org_id=eq.%s&id=eq.%s",
			ctx.GetOrgID(), nameOrID)
	}
	var conn pgrest.Connection
	if err := client.FetchOne().DecodeInto(&conn); err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &conn, nil
}

func (c *connections) FetchAll(ctx pgrest.OrgContext, opts ...*ConnectionOption) ([]pgrest.Connection, error) {
	safeEncodedOpts, err := urlEncodeOptions(opts)
	if err != nil {
		return nil, err
	}
	var items []pgrest.Connection
	err = pgrest.New("/connections?select=*,orgs(id,name),plugin_connections(config,plugins(name))&org_id=eq.%s&order=name.asc%s",
		ctx.GetOrgID(), safeEncodedOpts).
		List().
		DecodeInto(&items)
	if err != nil && err != pgrest.ErrNotFound {
		return nil, err
	}
	return items, nil
}

func (c *connections) Delete(ctx pgrest.OrgContext, name string) error {
	return pgrest.New("/connections?org_id=eq.%v&name=eq.%v", ctx.GetOrgID(),
		url.QueryEscape(name)).
		Delete().
		Error()
}

func (c *connections) Upsert(ctx pgrest.OrgContext, conn pgrest.Connection) error {
	var subType *string
	if conn.SubType != "" {
		subType = &conn.SubType
	}
	if conn.Status == "" {
		conn.Status = pgrest.ConnectionStatusOffline
	}
	return pgrest.New("/rpc/update_connection").RpcCreate(map[string]any{
		"id":         conn.ID,
		"org_id":     ctx.GetOrgID(),
		"name":       conn.Name,
		"agent_id":   toAgentID(conn.AgentID),
		"type":       conn.Type,
		"subtype":    subType,
		"command":    conn.Command,
		"envs":       conn.Envs,
		"status":     conn.Status,
		"managed_by": conn.ManagedBy,
		"tags":       conn.Tags,
	}).Error()
}

func toAgentID(agentID string) (v *string) {
	if _, err := uuid.Parse(agentID); err == nil {
		return &agentID
	}
	return
}

func (c *connections) UpdateStatusByName(ctx pgrest.OrgContext, connectionName, status string) error {
	err := pgrest.New("/connections?org_id=eq.%v&name=eq.%v", ctx.GetOrgID(), connectionName).
		Patch(map[string]any{"status": status}).
		Error()
	if err == pgrest.ErrNotFound {
		return nil
	}
	return err
}

func (c *connections) UpdateStatusByAgentID(ctx pgrest.OrgContext, agentID, status string) error {
	err := pgrest.New("/connections?org_id=eq.%v&agent_id=eq.%v", ctx.GetOrgID(), agentID).
		Patch(map[string]any{"status": status}).
		Error()
	if err == pgrest.ErrNotFound {
		return nil
	}
	return err
}

// UpdateAllToOffline update the status of all connection resources to offline
func (a *connections) UpdateAllToOffline() error {
	err := pgrest.New("/connections").Patch(map[string]any{"status": pgrest.ConnectionStatusOffline}).Error()
	if err == pgrest.ErrNotFound {
		return nil
	}
	return err
}
