package transportsystem

import (
	"fmt"

	pbsystem "github.com/hoophq/hoop/common/proto/system"
)

func Send(packetType, sid string, payload []byte) error {
	var obj any
	switch packetType {
	case pbsystem.ProvisionDBRolesResponse:
		obj = dbProvisionerStore.Pop(sid)
	default:
		return fmt.Errorf("received unknown system packet: %v", packetType)
	}
	if obj != nil {
		dataCh, ok := obj.(chan []byte)
		if !ok {
			return fmt.Errorf("unable to type cast channel, found=%T", obj)
		}
		defer close(dataCh)
		select {
		case dataCh <- payload:
			return nil
		default:
			return fmt.Errorf("failed to send payload (%v), to channel", len(payload))
		}
	}
	return fmt.Errorf("unable to find channel for sid %v", sid)
}
