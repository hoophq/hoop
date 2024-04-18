package transport

import (
	"fmt"

	"github.com/getsentry/sentry-go"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/monitoring"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	pbclient "github.com/runopsio/hoop/common/proto/client"
	pbgateway "github.com/runopsio/hoop/common/proto/gateway"
	"github.com/runopsio/hoop/gateway/transport/connectionrequests"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
	"github.com/runopsio/hoop/gateway/transport/streamclient"
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
		Payload: s.configurationData(stream.GetOrgName()),
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
		if _, err := proxyStream.PluginExecOnReceive(*pctx, pkt); err != nil {
			log.Warnf("plugin reject packet, err=%v", err)
			sentry.CaptureException(err)
			return status.Errorf(codes.Internal, "internal error, plugin reject packet")
		}

		if pb.PacketType(pkt.Type) == pbclient.SessionClose {
			var trackErr error
			if len(pkt.Payload) > 0 {
				trackErr = fmt.Errorf(string(pkt.Payload))
			}
			// it will make sure to run the disconnect plugin phase for both clients
			_ = proxyStream.Close(trackErr)
		}
		if err = proxyStream.Send(pkt); err != nil {
			log.With("sid", pctx.SID).Debugf("failed to send packet to proxy stream, err=%v", err)
		}
	}
}

func (s *Server) configurationData(orgName string) []byte {
	var transportConfigBytes []byte
	if s.PyroscopeIngestURL != "" {

		transportConfigBytes, _ = pb.GobEncode(monitoring.TransportConfig{
			Sentry: monitoring.SentryConfig{
				DSN:         s.AgentSentryDSN,
				OrgName:     orgName,
				Environment: monitoring.NormalizeEnvironment(s.IDProvider.ApiURL),
			},
			Profiler: monitoring.ProfilerConfig{
				PyroscopeServerAddress: s.PyroscopeIngestURL,
				PyroscopeAuthToken:     s.PyroscopeAuthToken,
				OrgName:                orgName,
				Environment:            monitoring.NormalizeEnvironment(s.IDProvider.ApiURL),
			},
		})
	}
	return transportConfigBytes
}
