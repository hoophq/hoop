package connectionstorage

import (
	"fmt"

	"github.com/runopsio/hoop/gateway/pgrest"
	pgconnections "github.com/runopsio/hoop/gateway/pgrest/connections"
	storage "github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"olympos.io/encoding/edn"
)

func Put(ctx *storage.Context, conn *types.Connection) error {
	_, err := ctx.Put(conn)
	return err
}

func GetOneByName(ctx *storage.Context, name string) (*types.Connection, error) {
	if pgrest.WithPostgres(ctx) {
		return pgconnections.New().FetchOneForExec(ctx, name)
	}
	payload := fmt.Sprintf(`{:query {
		:find [(pull ?connection [*])]
		:in [name org]
		:where [[?connection :connection/name name]
                [?connection :connection/org org]]}
		:in-args [%q %q]}`, name, ctx.OrgID)

	b, err := ctx.Query(payload)
	if err != nil {
		return nil, err
	}

	var connections [][]types.Connection
	if err := edn.Unmarshal(b, &connections); err != nil {
		return nil, err
	}

	if len(connections) == 0 {
		return nil, nil
	}

	conn := connections[0][0]
	return &conn, nil
}

func ListConnectionsByList(ctx *storage.Context, connectionNameList []string) (map[string]types.Connection, error) {
	var ednColBinding string
	for _, connName := range connectionNameList {
		ednColBinding += fmt.Sprintf("%q ", connName)
	}
	payload := fmt.Sprintf(`{:query {
		:find [(pull ?connection [*])]
		:in [org [connections ...]]
		:where [[?connection :connection/org org]
				[?connection :connection/name connections]]}
		:in-args [%q [%v]]}`, ctx.OrgID, ednColBinding)

	ednData, err := ctx.Query(payload)
	if err != nil {
		return nil, err
	}
	var connectionItems [][]types.Connection
	if err := edn.Unmarshal(ednData, &connectionItems); err != nil {
		return nil, err
	}

	itemMap := map[string]types.Connection{}
	for _, conn := range connectionItems {
		itemMap[conn[0].Name] = conn[0]
	}
	return itemMap, nil
}

func ConnectionsMapByID(ctx *storage.Context, connectionIDList []string) (map[string]types.Connection, error) {
	var connList []pgrest.Connection
	itemMap := map[string]types.Connection{}
	err := pgrest.New("/connections?org_id=eq.%s", ctx.OrgID).
		List().
		DecodeInto(&connList)
	if err != nil {
		if err == pgrest.ErrNotFound {
			return itemMap, nil
		}
		return nil, err
	}
	for _, conn := range connList {
		for _, connID := range connectionIDList {
			if conn.ID == connID {
				itemMap[connID] = types.Connection{
					Id:             conn.ID,
					OrgId:          conn.OrgID,
					Name:           conn.Name,
					Command:        conn.Command,
					Type:           conn.Type,
					SecretProvider: "database",
					SecretId:       "",
					CreatedById:    "",
					AgentId:        conn.AgentID,
				}
				break
			}
		}
	}
	return itemMap, nil

	// var ednColBinding string
	// for _, connID := range connectionIDList {
	// 	ednColBinding += fmt.Sprintf("%q ", connID)
	// }
	// payload := fmt.Sprintf(`{:query {
	// 	:find [(pull ?c [*])]
	// 	:in [org [connections ...]]
	// 	:where [[?c :connection/org org]
	// 			[?c :xt/id connections]]}
	// 	:in-args [%q [%v]]}`, ctx.OrgID, ednColBinding)

	// ednData, err := ctx.Query(payload)
	// if err != nil {
	// 	return nil, err
	// }
	// var connectionItems [][]types.Connection
	// if err := edn.Unmarshal(ednData, &connectionItems); err != nil {
	// 	return nil, err
	// }

	// itemMap := map[string]types.Connection{}
	// for _, conn := range connectionItems {
	// 	itemMap[conn[0].Id] = conn[0]
	// }
	// return itemMap, nil
}

func GetEntity(ctx *storage.Context, xtID string) (*types.Connection, error) {
	data, err := ctx.GetEntity(xtID)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
	}
	var obj types.Connection
	return &obj, edn.Unmarshal(data, &obj)
}
