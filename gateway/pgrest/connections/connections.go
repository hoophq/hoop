package pgconnections

import (
	"github.com/runopsio/hoop/gateway/pgrest"
	"github.com/runopsio/hoop/gateway/storagev2/types"
)

type connections struct{}

func New() *connections { return &connections{} }
func (c *connections) FetchOneForExec(ctx pgrest.OrgContext, name string) (*types.Connection, error) {
	var conn pgrest.Connection
	err := pgrest.New("/connections?select=*,orgs(id,name)&org_id=eq.%v&name=eq.%v",
		ctx.GetOrgID(), name).
		FetchOne().
		DecodeInto(&conn)
	if err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &types.Connection{
		Id:             conn.ID,
		OrgId:          conn.OrgID,
		Name:           conn.Name,
		Command:        conn.Command,
		Type:           conn.Type,
		SecretProvider: "database",
		SecretId:       "",
		CreatedById:    "",
		AgentId:        conn.AgentID,
	}, nil
}
