package transport

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	pbgateway "github.com/hoophq/hoop/common/proto/gateway"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/transport/connectionrequests"
	transportext "github.com/hoophq/hoop/gateway/transport/extensions"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	"github.com/hoophq/hoop/gateway/transport/streamclient"
	transportsystem "github.com/hoophq/hoop/gateway/transport/system"
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

		if handled := handleSystemPacketResponses(pctx, pkt); handled {
			continue
		}

		proxyStream := streamclient.GetProxyStream(pctx.SID)
		if proxyStream == nil {
			continue
		}
		extContext := transportext.Context{
			OrgID:                               pctx.OrgID,
			SID:                                 pctx.SID,
			AgentID:                             pctx.AgentID,
			ConnectionName:                      proxyStream.PluginContext().ConnectionName,
			ConnectionType:                      proxyStream.PluginContext().ConnectionType,
			ConnectionSubType:                   proxyStream.PluginContext().ConnectionSubType,
			ConnectionEnvs:                      proxyStream.PluginContext().ConnectionSecret,
			ConnectionJiraTransitionNameOnClose: proxyStream.PluginContext().ConnectionJiraTransitionNameOnClose,
			UserEmail:                           proxyStream.PluginContext().UserEmail,
			Verb:                                proxyStream.PluginContext().ClientVerb,
		}

		if err := transportext.OnReceive(extContext, pkt); err != nil {
			log.With("sid", pctx.SID).Warnf("extension reject packet, err=%v", err)
			return err
		}

		if _, err := proxyStream.PluginExecOnReceive(*pctx, pkt); err != nil {
			log.With("sid", pctx.SID).Warnf("plugin reject packet, err=%v", err)
			return status.Errorf(codes.Internal, "internal error, plugin reject packet")
		}

		switch pb.PacketType(pkt.Type) {
		case pbclient.SessionClose:
			// it will make sure to run the disconnect plugin phase for both clients
			_ = proxyStream.Close(buildErrorFromPacket(pctx.SID, pkt))
		case pbclient.SessionOpenOK:
			if proxyStream.PluginContext().ConnectionSubType == "ssh" {
				pkt.Spec[pb.SpecClientSSHHostKey] = []byte(appconfig.Get().SSHClientHostKey())
			}
		}

		if err = proxyStream.Send(pkt); err != nil {
			log.With("sid", pctx.SID).Debugf("failed to send packet to proxy stream, type=%v, err=%v", pkt.Type, err)
		}
	}
}

func handleSystemPacketResponses(pctx *plugintypes.Context, pkt *pb.Packet) (handled bool) {
	if strings.HasPrefix(pkt.Type, "Sys") {
		if err := transportsystem.Send(pkt.Type, pctx.SID, pkt.Payload); err != nil {
			log.With("sid", pctx.SID).Warnf("unable to send system packet, type=%v, reason=%v", pkt.Type, err)
		}
		return true
	}
	return
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
