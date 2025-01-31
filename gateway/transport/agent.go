package transport

import (
	"fmt"
	"strconv"

	"github.com/getsentry/sentry-go"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	pbgateway "github.com/hoophq/hoop/common/proto/gateway"
	"github.com/hoophq/hoop/gateway/transport/connectionrequests"
	transportext "github.com/hoophq/hoop/gateway/transport/extensions"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	"github.com/hoophq/hoop/gateway/transport/streamclient"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) subscribeAgent(stream *streamclient.AgentStream) (err error) {
	pluginContext := plugintypes.Context{
		OrgID:        stream.GetOrgID(),
		ClientOrigin: pb.ConnectionOriginAgent,
		AgentID:      stream.AgentID(),
		AgentName:    stream.AgentName(),
	}
	if err := stream.Save(); err != nil {
		log.With("agent", stream.AgentName()).Warnf("failed saving agent state, err=%v", err)
		_ = connectionrequests.AcceptProxyConnection(stream.GetOrgID(), stream.StreamAgentID(),
			fmt.Errorf("agent failed to connect, reason=%v", err))
		return err
	}
	// defer inside a function will bind any returned error
	defer func() { stream.Close(pluginContext, err) }()

	connectionrequests.AcceptProxyConnection(stream.GetOrgID(), stream.StreamAgentID(), nil)
	log.With("connection", stream.ConnectionName()).Infof("agent connected: %s", stream)
	_ = stream.Send(&pb.Packet{
		Type:    pbagent.GatewayConnectOK,
		Payload: nil,
	})
	return s.listenAgentMessages(&pluginContext, stream)
}

func (s *Server) listenAgentMessages(pctx *plugintypes.Context, stream *streamclient.AgentStream) error {
	ctx := stream.Context()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// receive data from stream
		pkt, err := stream.Recv()
		if err != nil {
			if status, ok := status.FromError(err); ok && status.Code() == codes.Canceled {
				log.With("id", stream.AgentID(), "name", stream.AgentName(), "connection", stream.ConnectionName()).
					Warnf("agent disconnected")
				return fmt.Errorf("agent has disconnected")
			}
			sentry.CaptureException(err)
			log.Errorf("received error from agent %v, err=%v", stream.AgentName(), err)
			return err
		}
		if pkt.Type == pbgateway.KeepAlive || pkt.Type == "KeepAlive" {
			continue
		}
		pctx.SID = string(pkt.Spec[pb.SpecGatewaySessionID])
		if pctx.SID == "" {
			log.Warnf("missing session id spec, skipping packet %v", pkt.Type)
			continue
		}
		proxyStream := streamclient.GetProxyStream(pctx.SID)
		if proxyStream == nil {
			continue
		}
		extContext := transportext.Context{
			OrgID:                               pctx.OrgID,
			SID:                                 pctx.SID,
			ConnectionName:                      proxyStream.PluginContext().ConnectionName,
			ConnectionJiraTransitionNameOnClose: proxyStream.PluginContext().ConnectionJiraTransitionNameOnClose,
			Verb:                                proxyStream.PluginContext().ClientVerb,
		}

		if err := transportext.OnReceive(extContext, pkt); err != nil {
			log.With("sid", pctx.SID).Warnf("extension reject packet, err=%v", err)
			return err
		}

		if _, err := proxyStream.PluginExecOnReceive(*pctx, pkt); err != nil {
			log.With("sid", pctx.SID).Warnf("plugin reject packet, err=%v", err)
			sentry.CaptureException(err)
			return status.Errorf(codes.Internal, "internal error, plugin reject packet")
		}

		if pb.PacketType(pkt.Type) == pbclient.SessionClose {
			// it will make sure to run the disconnect plugin phase for both clients
			_ = proxyStream.Close(buildErrorFromPacket(pctx.SID, pkt))
		}
		if err = proxyStream.Send(pkt); err != nil {
			log.With("sid", pctx.SID).Debugf("failed to send packet to proxy stream, err=%v", err)
		}
	}
}

func buildErrorFromPacket(sid string, pkt *pb.Packet) error {
	var exitCode *int
	exitCodeStr := string(pkt.Spec[pb.SpecClientExitCodeKey])
	ecode, err := strconv.Atoi(exitCodeStr)
	exitCode = &ecode
	if err != nil {
		exitCode = func() *int { v := 254; return &v }() // internal error code
	}

	log.With("sid", sid).Infof("session result, exit_code=%q, payload_length=%v",
		exitCodeStr, len(pkt.Payload))
	if len(pkt.Payload) == 0 && (exitCode == nil || *exitCode == 0) {
		return nil
	}

	return plugintypes.NewPacketErr(string(pkt.Payload), exitCode)
}
