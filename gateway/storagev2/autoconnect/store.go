package autoconnect

import (
	"bytes"
	"fmt"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"olympos.io/encoding/edn"
)

func GetEntity(ctx *storagev2.Context, xtID string) (*types.AutoConnect, error) {
	data, err := ctx.GetEntity(xtID)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
	}
	var obj types.AutoConnect
	return &obj, edn.Unmarshal(data, &obj)
}

func PutStatus(ctx *storagev2.Context, status string) (*types.AutoConnect, error) {
	if err := ctx.Validate(); err != nil {
		return nil, err
	}
	autoConnectID, err := uuid.NewRandomFromReader(bytes.NewBufferString(ctx.UserID))
	if err != nil {
		return nil, fmt.Errorf("failed generating auto connect id, err=%v", err)
	}
	obj, err := GetEntity(ctx, autoConnectID.String())
	if err != nil {
		return nil, fmt.Errorf("failed fetching auto connect entity, err=%v", err)
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
	_, err = ctx.Put(obj)
	return obj, err
}

func Put(ctx *storagev2.Context, obj *types.AutoConnect) error {
	_, err := ctx.Put(obj)
	return err
}
