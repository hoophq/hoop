package userstorage

import (
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"olympos.io/encoding/edn"
)

func UpdateLoginState(ctx *storagev2.Context, login *types.Login) error {
	_, err := ctx.Put(login)
	return err
}

func GetEntity(storageCtx *storagev2.Context, xtID string) (*types.User, error) {
	data, err := storageCtx.GetEntity(xtID)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
	}
	var obj types.User
	return &obj, edn.Unmarshal(data, &obj)
}
