package pgclientkeys

import (
	"github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/pgrest"
	"github.com/runopsio/hoop/gateway/storagev2/types"
)

type clientkeys struct{}

func New() *clientkeys { return &clientkeys{} }

func (c *clientkeys) ValidateDSN(dsnHash string) (*types.ClientKey, error) {
	var ck pgrest.ClientKey
	err := pgrest.New("/clientkeys?select=id,org_id,name,status&dsn_hash=eq.%s", dsnHash).
		FetchOne().
		DecodeInto(&ck)
	if err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &types.ClientKey{
		ID:        ck.ID,
		OrgID:     ck.OrgID,
		Name:      ck.Name,
		Active:    ck.Status == "active",
		DSNHash:   ck.DSNHash,
		AgentMode: proto.AgentModeEmbeddedType,
	}, nil
}
