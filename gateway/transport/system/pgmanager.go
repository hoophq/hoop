package transportsystem

import (
	"context"
	"fmt"

	"github.com/hoophq/hoop/common/proto"
	pbsystem "github.com/hoophq/hoop/common/proto/system"
	"github.com/hoophq/hoop/gateway/transport/streamclient"
	streamtypes "github.com/hoophq/hoop/gateway/transport/streamclient/types"
	"gopkg.in/yaml.v3"
)

func RunPgManagerPlan(agentID string, req *pbsystem.PgManagerPlanRequest) (*pbsystem.PgManagerPlanResponse, error) {
	st := streamclient.GetAgentStream(streamtypes.NewStreamID(agentID, ""))
	if st == nil {
		return nil, fmt.Errorf("agent stream not found for %v", agentID)
	}

	dispatchResp := dispatchPgManagerPlan(st, req)
	if dispatchResp == nil {
		return nil, fmt.Errorf("no response received from agent for pg manager plan request")
	}

	return dispatchResp, nil
}

func RunPgManagerApply(agentID string, req *pbsystem.PgManagerApplyRequest) (*pbsystem.PgManagerApplyResponse, error) {
	st := streamclient.GetAgentStream(streamtypes.NewStreamID(agentID, ""))
	if st == nil {
		return nil, fmt.Errorf("agent stream not found for %v", agentID)
	}

	dispatchResp := dispatchPgManagerApply(st, req)
	if dispatchResp == nil {
		return nil, fmt.Errorf("no response received from agent for pg manager plan request")
	}

	// TODO: save the session
	return dispatchResp, nil
}

func dispatchPgManagerPlan(st *streamclient.AgentStream, req *pbsystem.PgManagerPlanRequest) *pbsystem.PgManagerPlanResponse {
	dataCh := make(chan []byte)
	systemStore.Set(req.SID, dataCh)
	defer func() {
		systemStore.Del(req.SID)
		close(dataCh)
	}()

	payload, pbType, err := pbsystem.NewPgManagerPlanRequest(req)
	if err != nil {
		return pbsystem.NewPgManagerPlanError(req.SID, "failed encoding request: %v", err)
	}

	if err := st.Send(&proto.Packet{
		Type:    pbType,
		Payload: payload,
		Spec:    map[string][]byte{proto.SpecGatewaySessionID: []byte(req.SID)},
	}); err != nil {
		return pbsystem.NewPgManagerPlanError(req.SID, "failed sending request to agent: %v", err)
	}

	timeoutCtx, cancelFn := context.WithTimeout(context.Background(), resourceManagerTimeout)
	defer cancelFn()
	select {
	case payload := <-dataCh:
		var resp pbsystem.PgManagerPlanResponse
		if err := yaml.Unmarshal(payload, &resp); err != nil {
			return pbsystem.NewPgManagerPlanError(req.SID, "failed decoding agent response: %v", err)
		}
		return &resp
	case <-timeoutCtx.Done():
		return pbsystem.NewPgManagerPlanError(req.SID,
			"timeout (%v) waiting for response from agent %v/%v",
			resourceManagerTimeout, st.AgentName(), st.AgentVersion())
	}
}

func dispatchPgManagerApply(st *streamclient.AgentStream, req *pbsystem.PgManagerApplyRequest) *pbsystem.PgManagerApplyResponse {
	dataCh := make(chan []byte)
	systemStore.Set(req.SID, dataCh)
	defer func() {
		systemStore.Del(req.SID)
		close(dataCh)
	}()

	payload, pbType, err := pbsystem.NewPgManagerApplyRequest(req)
	if err != nil {
		return pbsystem.NewPgManagerApplyError(req.SID, "failed encoding request: %v", err)
	}

	if err := st.Send(&proto.Packet{
		Type:    pbType,
		Payload: payload,
		Spec:    map[string][]byte{proto.SpecGatewaySessionID: []byte(req.SID)},
	}); err != nil {
		return pbsystem.NewPgManagerApplyError(req.SID, "failed sending request to agent: %v", err)
	}

	timeoutCtx, cancelFn := context.WithTimeout(context.Background(), resourceManagerTimeout)
	defer cancelFn()
	select {
	case payload := <-dataCh:
		var resp pbsystem.PgManagerApplyResponse
		if err := yaml.Unmarshal(payload, &resp); err != nil {
			return pbsystem.NewPgManagerApplyError(req.SID, "failed decoding agent response: %v", err)
		}
		return &resp
	case <-timeoutCtx.Done():
		return pbsystem.NewPgManagerApplyError(req.SID,
			"timeout (%v) waiting for response from agent %v/%v",
			resourceManagerTimeout, st.AgentName(), st.AgentVersion())
	}
}
