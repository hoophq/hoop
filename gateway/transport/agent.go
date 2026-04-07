package transport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/hoophq/hoop/common/log"
	pgtypes "github.com/hoophq/hoop/common/pgtypes"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	pbgateway "github.com/hoophq/hoop/common/proto/gateway"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
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

		if handled := handleSessionAnalyzerMetricsPacket(pctx, pkt); handled {
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
			updateGuardRailsInfoFromPacket(pctx, pkt)
			// it will make sure to run the disconnect plugin phase for both clients
			_ = proxyStream.Close(buildErrorFromPacket(pctx.SID, pkt))
		case pbclient.SessionOpenOK:
			if proxyStream.PluginContext().ConnectionSubType == "ssh" {
				pkt.Spec[pb.SpecClientSSHHostKey] = []byte(appconfig.Get().SSHClientHostKey())
			}
<<<<<<< perotto/eng-300-show-warning-on-hoop-connect-if-cli-and-agents-are-in
			if agentVersion := stream.GetMeta("version"); agentVersion != "" {
				pkt.Spec[pb.SpecAgentVersion] = []byte(agentVersion)
			}
=======
		case pbclient.PGConnectionWrite:
			rewritePGGuardRailsErrorPacket(pkt)
>>>>>>> main
		}

		if err = proxyStream.Send(pkt); err != nil {
			log.With("sid", pctx.SID).Debugf("failed to send packet to proxy stream, type=%v, err=%v", pkt.Type, err)
		}
	}
}

func updateGuardRailsInfoFromPacket(pctx *plugintypes.Context, pkt *pb.Packet) {
	if rawInfo := pkt.Spec[pb.SpecClientGuardRailsInfoKey]; len(rawInfo) > 0 {
		var guardRailsData []models.SessionGuardRailsInfo
		if err := json.Unmarshal(rawInfo, &guardRailsData); err != nil {
			log.With("sid", pctx.SID).Errorf("unable to unmarshal guardrails info from session close, reason=%v", err)
		} else if err := models.UpdateSessionGuardRailsInfo(pctx.OrgID, pctx.SID, rawInfo); err != nil {
			log.With("sid", pctx.SID).Errorf("unable to save guardrails info from session close, reason=%v", err)
		}
	}
}

func rewritePGGuardRailsErrorPacket(pkt *pb.Packet) {
	rawInfo := pkt.Spec[pb.SpecClientGuardRailsInfoKey]
	if len(rawInfo) == 0 || len(pkt.Payload) == 0 {
		return
	}
	msg, ok := buildLegacyGuardRailErrorMessage(rawInfo)
	if !ok || msg == "" {
		return
	}

	decoded, err := pgtypes.Decode(bytes.NewBuffer(pkt.Payload))
	if err != nil || decoded == nil {
		return
	}
	if decoded.Type() != pgtypes.ServerErrorResponse {
		return
	}

	pkt.Payload = pgtypes.NewError("%s", msg).Encode()
}

func handleSessionAnalyzerMetricsPacket(pctx *plugintypes.Context, pkt *pb.Packet) (handled bool) {
	if pb.PacketType(pkt.Type) != pbclient.SessionAnalyzerMetrics {
		return false
	}

	handled = true
	log.With("sid", pctx.SID).Debugf("received analyzer metrics packet, length=%v", len(pkt.Payload))

	var data map[string]int64
	if err := json.Unmarshal(pkt.Payload, &data); err != nil {
		log.With("sid", pctx.SID).Errorf("unable to unmarshal analyzer metrics packet, reason=%v", err)
		return
	}

	if err := models.UpdateSessionAnalyzerMetrics(pctx.OrgID, pctx.SID, data); err != nil {
		log.With("sid", pctx.SID).Errorf("unable to save analyzer metrics packet, reason=%v", err)
	}

	if err := models.IncrementSessionAnalyzedMetrics(models.DB, pctx.SID, data); err != nil {
		log.With("sid", pctx.SID).Errorf("unable to increment analyzed metrics, reason=%v", err)
	}

	return
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

	errMsg := string(pkt.Payload)
	if rawInfo := pkt.Spec[pb.SpecClientGuardRailsInfoKey]; len(rawInfo) > 0 {
		if msg, ok := buildLegacyGuardRailErrorMessage(rawInfo); ok {
			errMsg = msg
		}
	}

	return plugintypes.NewPacketErr(errMsg, exitCode)
}

func buildLegacyGuardRailErrorMessage(rawInfo []byte) (string, bool) {
	var items []models.SessionGuardRailsInfo
	if err := json.Unmarshal(rawInfo, &items); err != nil || len(items) == 0 {
		return "", false
	}

	parts := make([]string, 0, len(items))
	for _, item := range items {
		direction := "InputRules"
		if strings.EqualFold(item.Direction, "output") {
			direction = "OutputRules"
		}
		scope := fmt.Sprintf("[%s]", direction)
		if item.RuleName != "" {
			scope = fmt.Sprintf("[%s:%s]", direction, item.RuleName)
		}

		ruleType := item.Rule.Type
		if len(item.Rule.Words) > 0 {
			parts = append(parts,
				fmt.Sprintf("validation error, match guard rail %s rule, type=%s, words=%v", scope, ruleType, item.Rule.Words))
			continue
		}
		if item.Rule.PatternRegex != "" {
			parts = append(parts,
				fmt.Sprintf("validation error, match guard rail %s rule, type=%s, patterns=%s", scope, ruleType, item.Rule.PatternRegex))
			continue
		}
		parts = append(parts, fmt.Sprintf("validation error, match guard rail %s rule, type=%s", scope, ruleType))
	}

	return "Blocked by the following Hoop Guardrails Rules: " + strings.Join(parts, ", "), true
}
