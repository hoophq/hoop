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

var bareExecTimeout = time.Minute * 5

// BareExec executes a template
func BareExec(req *pbsystem.BareExecRequest) *pbsystem.BareExecResponse {
	st := streamclient.GetAgentStream(streamtypes.NewStreamID(req.AgentID, ""))
	if st == nil {
		return &pbsystem.BareExecResponse{
			SessionID: req.SID,
			Status:    pbsystem.StatusFailedType,
			Output:    fmt.Sprintf("agent stream not found for %v", req.AgentID),
		}
	}
	return dispatchBareExec(st, req)
}

func dispatchBareExec(st *streamclient.AgentStream, req *pbsystem.BareExecRequest) *pbsystem.BareExecResponse {
	dataCh := make(chan []byte)
	systemStore.Set(req.SID, dataCh)
	defer func() {
		systemStore.Del(req.SID)
		close(dataCh)
	}()

	payload, pbType, err := pbsystem.NewBareExecRequest(req)
	if err != nil {
		return &pbsystem.BareExecResponse{
			SessionID: req.SID,
			Status:    pbsystem.StatusFailedType,
			Output:    fmt.Sprintf("failed encoding request: %v", err),
		}
	}

	if err := st.Send(&proto.Packet{
		Type:    pbType,
		Payload: payload,
		Spec:    map[string][]byte{proto.SpecGatewaySessionID: []byte(req.SID)},
	}); err != nil {
		return &pbsystem.BareExecResponse{
			SessionID: req.SID,
			Status:    pbsystem.StatusFailedType,
			Output:    fmt.Sprintf("failed sending request to agent: %v", err),
		}
	}

	timeoutCtx, cancelFn := context.WithTimeout(context.Background(), bareExecTimeout)
	defer cancelFn()
	select {
	case payload := <-dataCh:
		var resp pbsystem.BareExecResponse
		if err := json.Unmarshal(payload, &resp); err != nil {
			return &pbsystem.BareExecResponse{
				SessionID: req.SID,
				Status:    pbsystem.StatusFailedType,
				Output:    fmt.Sprintf("failed decoding agent response: %v", err),
			}
		}
		return &resp
	case <-timeoutCtx.Done():
		return &pbsystem.BareExecResponse{
			SessionID: req.SID,
			Status:    pbsystem.StatusFailedType,
			Output:    fmt.Sprintf("timeout (%v) waiting for response from agent %v/%v", bareExecTimeout, st.AgentName(), st.AgentVersion()),
		}
	}
}
