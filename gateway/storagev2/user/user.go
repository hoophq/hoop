package user

import (
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
)

func UpdateLoginState(ctx *storagev2.Context, login *types.Login) error {
	_, err := ctx.Put(login)
	return err
}
