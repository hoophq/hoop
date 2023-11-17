package transportv2

import (
	"fmt"
	"io"

	"github.com/getsentry/sentry-go"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	pbclient "github.com/runopsio/hoop/common/proto/client"
	pbgateway "github.com/runopsio/hoop/common/proto/gateway"
	"github.com/runopsio/hoop/gateway/apiclient"
	"github.com/runopsio/hoop/gateway/pgrest"
	"github.com/runopsio/hoop/gateway/transportv2/auditfs"
	"github.com/runopsio/hoop/gateway/transportv2/memorystreams"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func SubscribeAgent(ctx *AgentContext, grpcStream pb.Transport_ConnectServer) error {
	stream := memorystreams.NewWrapperStream(ctx.Agent.OrgID, grpcStream)
	memorystreams.SetAgent(ctx.Agent.ID, stream)
	defer func() {
		memorystreams.DelAgent(ctx.Agent.ID)
		// best effort
		publishAgentDisconnect(ctx.ApiURL, ctx.Agent.ID)
	}()
	log.With("id", ctx.Agent.ID).Infof("agent connected: %s", ctx.Agent)
	_ = stream.Send(&pb.Packet{
		Type: pbagent.GatewayConnectOK,
		// TODO: add pyroscope & sentry monitoring
		Payload: nil,
	})
	// TODO: disconnect all clients associated with this agent in memory only
	// it should populate the disconnect reason when is a stateful connection (connect)
	return listenAgentMessages(ctx, stream)
}

func listenAgentMessages(ctx *AgentContext, stream memorystreams.Wrapper) error {
	sctx := stream.Context()
	for {
		select {
		case <-sctx.Done():
			return sctx.Err()
		default:
		}
		// receive data from stream
		pkt, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				return fmt.Errorf("agent %v disconnected, end-of-file stream", ctx.Agent.Name)
			}
			if status, ok := status.FromError(err); ok && status.Code() == codes.Canceled {
				log.Warnf("id=%v, name=%v - agent disconnected", ctx.Agent.ID, ctx.Agent.Name)
				return fmt.Errorf("agent %v disconnected, reason=%v", ctx.Agent.Name, err)
			}
			sentry.CaptureException(err)
			log.Errorf("received error from agent %v, err=%v", ctx.Agent.Name, err)
			return err
		}
		if pkt.Type == pbgateway.KeepAlive {
			continue
		}
		sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
		log.With("sid", sessionID).Debugf("receive agent packet type [%s]", pkt.Type)
		if err := auditfs.Write(sessionID, pkt); err != nil {
			log.Warnf("failed auditing output packet, err=%v", err)
			// TODO: send a packet to the client informing the problem
			continue
		}

		if clientStream := memorystreams.GetClient(sessionID); clientStream != nil {
			_ = clientStream.Send(pkt)
		}

		if pb.PacketType(pkt.Type) == pbclient.SessionClose {
			// TODO: save this error to audit
			var trackErr error
			if len(pkt.Payload) > 0 {
				trackErr = fmt.Errorf(string(pkt.Payload))
			}
			_ = trackErr
			memorystreams.DisconnectClient(sessionID, nil)
			// now it's safe to remove the session from memory
			err = auditfs.Close(sessionID, apiclient.New(ctx.BearerToken))
			log.With("sid", sessionID, "agent", ctx.Agent.Name).
				Infof("closing session, success=%v, err=%v", err == nil, err)
		}
	}
}

func publishAgentDisconnect(apiURL, agentID string) error {
	return pgrest.New("/agents?id=eq.%s", agentID).Patch(
		map[string]any{"status": "DISCONNECTED"}).Error()
	// reqBody := apitypes.AgentAuthRequest{Status: "DISCONNECTED"}
	// _, err := apiclient.New(bearerToken).AuthAgent(reqBody)
	// return err
}
