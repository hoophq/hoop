package transportsys

import (
	"context"
	"encoding/json"
	"fmt"
	"libhoop/memory"
	"strings"
	"time"

	"github.com/hoophq/hoop/common/proto"
	pbsys "github.com/hoophq/hoop/common/proto/sys"
	"github.com/hoophq/hoop/gateway/transport/streamclient"
	streamtypes "github.com/hoophq/hoop/gateway/transport/streamclient/types"
)

var (
	store = memory.New()
)

type ProtoPacket struct {
	Payload []byte
	Spec    map[string][]byte
}

func Send(sid string, payload []byte) error {
	// TODO: check the amount of memory it has
	if obj := store.Pop(sid); obj != nil {
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

func RunDBProvisioner(agentID string, req *pbsys.DBProvisionerRequest) *pbsys.DBProvisionerResponse {
	st := streamclient.GetAgentStream(streamtypes.NewStreamID(agentID, ""))
	if st == nil {
		return pbsys.NewError(req.SID, "agent stream not found for %v", agentID)
	}

	dataCh := make(chan []byte)
	store.Set(req.SID, dataCh)

	payload, pbType, _ := pbsys.NewDbProvisionerRequest(req)
	err := st.Send(&proto.Packet{
		Type:    pbType,
		Payload: payload,
		Spec:    map[string][]byte{proto.SpecGatewaySessionID: []byte(req.SID)}},
	)

	if err != nil {
		store.Del(req.SID)
		close(dataCh)
		return pbsys.NewError(req.SID, "failed sending provision request packet, reason=%v", err)
	}

	timeoutCtx, cancelFn := context.WithTimeout(context.Background(), time.Second*120)
	defer cancelFn()
	select {
	case payload := <-dataCh:
		var resp pbsys.DBProvisionerResponse
		if err := json.Unmarshal(payload, &resp); err != nil {
			return pbsys.NewError(req.SID, "unable to decode response: %v", err)
		}
		redactMessage(req, &resp)
		return &resp
	case <-timeoutCtx.Done():
		return pbsys.NewError(req.SID, "timeout waiting for a response")
	}
}

func redactMessage(req *pbsys.DBProvisionerRequest, resp *pbsys.DBProvisionerResponse) {
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
