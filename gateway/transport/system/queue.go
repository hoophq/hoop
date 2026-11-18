package transportsystem

import (
	"fmt"

	"github.com/hoophq/hoop/common/memory"
	pbsystem "github.com/hoophq/hoop/common/proto/system"
)

var systemStore = memory.New()

type pendingResponse struct {
	orgID string
	ch    chan []byte
}

// awaitResponse registers a request that only an agent of orgID can answer.
func awaitResponse(orgID, sid string) (chan []byte, func()) {
	ch := make(chan []byte)
	systemStore.Set(sid, pendingResponse{orgID: orgID, ch: ch})
	return ch, func() {
		systemStore.Del(sid)
		close(ch)
	}
}

// Send delivers the response of an agent of orgID to the request it answers.
func Send(orgID, packetType, sid string, payload []byte) error {
	switch packetType {
	case pbsystem.ProvisionDBRolesResponse, pbsystem.RunbookHookResponseType,
		pbsystem.BareExecResponseType, pbsystem.PgManagerPlanResponseType, pbsystem.PgManagerApplyResponseType:
	default:
		return fmt.Errorf("received unknown system packet: %v", packetType)
	}
	pending, ok := systemStore.Get(sid).(pendingResponse)
	if !ok || orgID == "" || pending.orgID != orgID {
		return fmt.Errorf("unable to find channel")
	}
	if systemStore.Pop(sid) == nil {
		return fmt.Errorf("unable to find channel")
	}
	select {
	case pending.ch <- payload:
		return nil
	default:
		return fmt.Errorf("failed to send payload (%v), to channel", len(payload))
	}
}
