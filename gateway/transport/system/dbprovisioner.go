package transportsystem

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/hoophq/hoop/common/memory"
	"github.com/hoophq/hoop/common/proto"
	pbsystem "github.com/hoophq/hoop/common/proto/system"
	"github.com/hoophq/hoop/gateway/transport/streamclient"
	streamtypes "github.com/hoophq/hoop/gateway/transport/streamclient/types"
)

var dbProvisionerStore = memory.New()

func RunDBProvisioner(agentID string, req *pbsystem.DBProvisionerRequest) *pbsystem.DBProvisionerResponse {
	st := streamclient.GetAgentStream(streamtypes.NewStreamID(agentID, ""))
	if st == nil {
		return pbsystem.NewError(req.SID, "agent stream not found for %v", agentID)
	}

	dataCh := make(chan []byte)
	dbProvisionerStore.Set(req.SID, dataCh)
	defer dbProvisionerStore.Del(req.SID)

	payload, pbType, _ := pbsystem.NewDbProvisionerRequest(req)
	err := st.Send(&proto.Packet{
		Type:    pbType,
		Payload: payload,
		Spec:    map[string][]byte{proto.SpecGatewaySessionID: []byte(req.SID)}},
	)

	if err != nil {
		close(dataCh)
		return pbsystem.NewError(req.SID, "failed sending provision request packet, reason=%v", err)
	}

	timeoutCtx, cancelFn := context.WithTimeout(context.Background(), time.Second*600)
	defer cancelFn()
	select {
	case payload := <-dataCh:
		var resp pbsystem.DBProvisionerResponse
		if err := json.Unmarshal(payload, &resp); err != nil {
			return pbsystem.NewError(req.SID, "unable to decode response: %v", err)
		}
		redactMessage(req, &resp)
		return &resp
	case <-timeoutCtx.Done():
		return pbsystem.NewError(req.SID, "timeout waiting for a response")
	}
}

func redactMessage(req *pbsystem.DBProvisionerRequest, resp *pbsystem.DBProvisionerResponse) {
	if strings.ContainsAny(resp.Message, req.MasterPassword) {
		resp.Message = strings.ReplaceAll(resp.Message, req.MasterPassword, "REDACTED")
	}

	for i, r := range resp.Result {
		if strings.ContainsAny(r.Message, req.MasterPassword) {
			r.Message = strings.ReplaceAll(r.Message, req.MasterPassword, "REDACTED")
			resp.Result[i] = r
		}
	}
}
