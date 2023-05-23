package autoconnect

import (
	"bytes"
	"fmt"

	"github.com/google/uuid"
	corev2 "github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"olympos.io/encoding/edn"
)

type Model struct {
	store *corev2.Store
}

func New(store *corev2.Store) *Model {
	return &Model{
		store: store,
	}
}

func (m *Model) GetEntity(xtID string) (*types.AutoConnect, error) {
	data, err := m.store.GetEntity(xtID)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
	}
	var obj types.AutoConnect
	return &obj, edn.Unmarshal(data, &obj)
}

func (m *Model) PutStatus(ctx *types.UserContext, status string) error {
	if err := ctx.Validate(); err != nil {
		return err
	}
	autoConnectID, err := uuid.NewRandomFromReader(bytes.NewBufferString(ctx.UserID))
	if err != nil {
		return fmt.Errorf("failed generating auto connect id, err=%v", err)
	}
	obj, err := m.GetEntity(autoConnectID.String())
	if err != nil {
		return fmt.Errorf("failed fetching auto connect entity, err=%v", err)
	}
	if obj == nil {
		obj = &types.AutoConnect{
			ID:     autoConnectID.String(),
			OrgId:  ctx.OrgID,
			Status: status,
			User:   "foo",
		}
	} else {
		obj.Status = status
	}
	_, err = m.store.Put(obj)
	return err
}
