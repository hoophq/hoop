package transportsystem

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hoophq/hoop/common/proto"
	pbsystem "github.com/hoophq/hoop/common/proto/system"
	"github.com/hoophq/hoop/gateway/transport/streamclient"
	streamtypes "github.com/hoophq/hoop/gateway/transport/streamclient/types"
)

var hookTimeoutRequest = time.Second * 15

func RunRunbookHook(agentID string, req *pbsystem.RunbookHookRequest) *pbsystem.RunbookHookResponse {
	st := streamclient.GetAgentStream(streamtypes.NewStreamID(agentID, ""))
	if st == nil {
		return newRunbookHookErr(req.SID, "agent stream not found for %v", agentID)
	}

	dataCh := make(chan []byte)
	systemStore.Set(req.ID, dataCh)
	defer func() {
		systemStore.Del(req.ID)
		close(dataCh)
	}()

	payload, _ := json.Marshal(req)
	err := st.Send(&proto.Packet{
		Type:    pbsystem.RunbookHookRequestType,
		Payload: payload,
		Spec:    map[string][]byte{proto.SpecGatewaySessionID: []byte(req.ID)},
	})
	if err != nil {
		return newRunbookHookErr(req.ID, "failed sending provision request packet, reason=%v", err)
	}

	timeoutCtx, cancelFn := context.WithTimeout(context.Background(), hookTimeoutRequest)
	defer cancelFn()
	select {
	case payload := <-dataCh:
		var resp pbsystem.RunbookHookResponse
		if err := json.Unmarshal(payload, &resp); err != nil {
			return newRunbookHookErr(req.ID, "unable to decode response: %v", err)
		}
		return &resp
	case <-timeoutCtx.Done():
		return newRunbookHookErr(req.ID, "timeout (%v) waiting for a response from agent, name=%v, version=%v",
			hookTimeoutRequest.String(), st.AgentName(), st.AgentVersion())
	}
}

func newRunbookHookErr(sid, format string, a ...any) *pbsystem.RunbookHookResponse {
	return &pbsystem.RunbookHookResponse{
		ID:       sid,
		ExitCode: -2,
		Output:   fmt.Sprintf(format, a...),
	}
}
