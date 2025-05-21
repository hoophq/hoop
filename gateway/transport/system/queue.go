package transportsystem

import (
	"fmt"

	"github.com/hoophq/hoop/common/memory"
	pbsystem "github.com/hoophq/hoop/common/proto/system"
)

var systemStore = memory.New()

func Send(packetType, sid string, payload []byte) error {
	var obj any
	switch packetType {
	case pbsystem.ProvisionDBRolesResponse, pbsystem.RunbookHookResponseType:
		obj = systemStore.Pop(sid)
	default:
		return fmt.Errorf("received unknown system packet: %v", packetType)
	}
	if obj != nil {
		dataCh, ok := obj.(chan []byte)
		if !ok {
			return fmt.Errorf("unable to type cast channel, found=%T", obj)
		}
		select {
		case dataCh <- payload:
			return nil
		default:
			return fmt.Errorf("failed to send payload (%v), to channel", len(payload))
		}
	}
	return fmt.Errorf("unable to find channel")
}
