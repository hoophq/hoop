package connectionstorage

import (
	"fmt"

	storage "github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"olympos.io/encoding/edn"
)

func GetOneByName(storageCtx *storage.Context, name string) (*types.Connection, error) {
	payload := fmt.Sprintf(`{:query {
		:find [(pull ?connection [*])] 
		:in [name org]
		:where [[?connection :connection/name name]
                [?connection :connection/org org]]}
		:in-args [%q %q]}`, name, storageCtx.OrgID)

	b, err := storageCtx.Query(payload)
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
