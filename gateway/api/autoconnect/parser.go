package apiautoconnect

import (
	"github.com/runopsio/hoop/gateway/storagev2/types"
)

func parseAutoConnection(model *types.AutoConnect) *AutoConnect {
	return &AutoConnect{
		Status: model.Status,
		// Time: model.
		Client: model.Client,
	}
}
