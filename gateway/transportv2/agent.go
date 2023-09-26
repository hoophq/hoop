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
	apitypes "github.com/runopsio/hoop/gateway/apiclient/types"
	"github.com/runopsio/hoop/gateway/transportv2/auditfs"
	"github.com/runopsio/hoop/gateway/transportv2/memorystreams"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func SubscribeAgent(ctx *AgentContext, stream pb.Transport_ConnectServer) error {
	clientOrigin := pb.ConnectionOriginAgent
	memorystreams.SetAgent(ctx.Agent.ID, stream)
	log.With("id", ctx.Agent.ID).Infof("agent connected: %s", ctx.Agent)
	_ = stream.Send(&pb.Packet{
		Type: pbagent.GatewayConnectOK,
		// TODO: add pyroscope & sentry monitoring
		Payload: nil,
	})
	startDisconnectClientSink(ctx.Agent.ID, clientOrigin, func(err error) {
		// TODO: send disconnect to all sessions in memory with this agent
		memorystreams.DelAgent(ctx.Agent.ID)
	})
	err := listenAgentMessages(ctx, stream)
	defer func() {
		disconnectClient(ctx.Agent.ID, err)
		_ = publishAgentDisconnect(ctx.ApiURL, ctx.BearerToken)
	}()

	return err
}
func listenAgentMessages(ctx *AgentContext, stream pb.Transport_ConnectServer) error {
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

		if pb.PacketType(pkt.Type) == pbclient.SessionClose {
			if sessionID := pkt.Spec[pb.SpecGatewaySessionID]; len(sessionID) > 0 {
				var trackErr error
				if len(pkt.Payload) > 0 {
					trackErr = fmt.Errorf(string(pkt.Payload))
				}
				disconnectClient(string(sessionID), trackErr)
				// now it's safe to remove the session from memory
				auditfs.Close(string(sessionID), apiclient.New(ctx.ApiURL, ctx.BearerToken))
			}
		}

		if clientStream := memorystreams.GetClient(sessionID); clientStream != nil {
			_ = clientStream.Send(pkt)
		}
	}
}

func publishAgentDisconnect(apiURL, bearerToken string) error {
	reqBody := apitypes.AgentAuthRequest{Status: "DISCONNECTED"}
	_, err := apiclient.New(apiURL, bearerToken).AuthAgent(reqBody)
	return err
}
