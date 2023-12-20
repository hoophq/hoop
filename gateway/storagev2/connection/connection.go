package connectionstorage

import (
	"github.com/runopsio/hoop/gateway/pgrest"
	pgconnections "github.com/runopsio/hoop/gateway/pgrest/connections"
	storage "github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
)

func Put(ctx *storage.Context, conn *types.Connection) error {
	return pgconnections.New().Upsert(ctx, pgrest.Connection{
		ID:            conn.Id,
		OrgID:         ctx.OrgID,
		AgentID:       conn.AgentId,
		LegacyAgentID: conn.AgentId,
		Name:          conn.Name,
		Command:       conn.Command,
		Type:          string(conn.Type),
	})
}

func GetOneByName(ctx *storage.Context, name string) (*types.Connection, error) {
	return pgconnections.New().FetchOneForExec(ctx, name)
}

func ListConnectionsByList(ctx *storage.Context, connectionNameList []string) (map[string]types.Connection, error) {
	return pgconnections.New().FetchByNames(ctx, connectionNameList)
}

func ConnectionsMapByID(ctx *storage.Context, connectionIDList []string) (map[string]types.Connection, error) {
	return pgconnections.New().FetchByIDs(ctx, connectionIDList)
}
