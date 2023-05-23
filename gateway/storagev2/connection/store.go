package connection

import (
	corev2 "github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
)

type Model struct {
	store *corev2.Store
}

func New(store *corev2.Store) *Model { return &Model{store: store} }

func (m *Model) Get(ctx *types.UserContext, id string) (*types.Connection, error) {
	return nil, nil
}
