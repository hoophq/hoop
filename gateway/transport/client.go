package transport

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/hoophq/hoop/common/apiutils"
	"github.com/hoophq/hoop/common/grpc"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	pbgateway "github.com/hoophq/hoop/common/proto/gateway"
	"github.com/hoophq/hoop/gateway/analytics"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/guardrails"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/transport/connectionrequests"
	transportext "github.com/hoophq/hoop/gateway/transport/extensions"
	pluginslack "github.com/hoophq/hoop/gateway/transport/plugins/slack"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	"github.com/hoophq/hoop/gateway/transport/streamclient"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func requestProxyConnection(stream *streamclient.ProxyStream) error {
	pctx := stream.PluginContext()
	if !stream.IsAgentOnline() {
		log.With("user", pctx.UserEmail, "agentname", pctx.AgentName, "connection", pctx.ConnectionName).
			Infof("requesting connection with remote agent")
		err := connectionrequests.RequestProxyConnection(connectionrequests.Info{
			OrgID:          pctx.GetOrgID(),
			AgentID:        pctx.AgentID,
			AgentMode:      pctx.AgentMode,
			ConnectionName: pctx.ConnectionName,
			SID:            pctx.SID,
		})
		switch err {
		case connectionrequests.ErrConnTimeout:
			if err := stream.ContextCauseError(); err != nil {
				log.With("user", pctx.UserEmail, "agentname", pctx.AgentName, "connection", pctx.ConnectionName).
					Infof("timeout requesting connection with agent, reason=%v", err)
				return err
			}
			log.With("user", pctx.UserEmail, "agentname", pctx.AgentName, "connection", pctx.ConnectionName).Infof("agent offline")
			return pb.ErrAgentOffline
		case nil:
			log.With("user", pctx.UserEmail, "agentname", pctx.AgentName, "connection", pctx.ConnectionName).Infof("connection established with agent")
		default:
			log.With("user", pctx.UserEmail, "agentname", pctx.AgentName, "connection", pctx.ConnectionName).
				Warnf("failed to establish connection with agent, reason=%v", err)
			return status.Errorf(codes.Aborted, err.Error())
		}
	}
	return nil
}

func (s *Server) subscribeClient(stream *streamclient.ProxyStream) (err error) {
	clientVerb := stream.GetMeta("verb")
	clientOrigin := stream.GetMeta("origin")
	pctx := stream.PluginContext()
	if err := requestProxyConnection(stream); err != nil {
		return err
	}

	if err := validateConnectionType(clientVerb, pctx); err != nil {
		return err
	}

	if err := stream.Save(); err != nil {
		log.With("connection", pctx.ConnectionName).Error(err)
		return err
	}
	// The webapp client must keep the stream open to be able
	// to process packets when the grpc client disconnects.
	// The stream will be closed when receiving a SessionClose packet
	// from the agent or when the stream process manager closes it.
	if clientOrigin == pb.ConnectionOriginClient {
		// defer inside a function will bind any returned error
		defer func() { stream.Close(err) }()
	}
	eventName := analytics.EventGrpcExec
	if clientVerb == pb.ClientVerbConnect {
		eventName = analytics.EventGrpcConnect
	}
	var cmdEntrypoint string
	if len(pctx.ConnectionCommand) > 0 {
		cmdEntrypoint = pctx.ConnectionCommand[0]
	}

	userAgent := apiutils.NormalizeUserAgent(func(key string) []string {
		return []string{stream.GetMeta("user-agent")}
	})
	analytics.New().Track(pctx.UserID, eventName, map[string]any{
		"org-id":                pctx.OrgID,
		"connection-name":       pctx.ConnectionName,
		"connection-type":       pctx.ConnectionType,
		"connection-subtype":    pctx.ConnectionSubType,
		"connection-entrypoint": cmdEntrypoint,
		"client-version":        stream.GetMeta("version"),
		"platform":              stream.GetMeta("platform"),
		"user-agent":            userAgent,
		"origin":                clientOrigin,
		"verb":                  clientVerb,
	})

	logAttrs := []any{"sid", pctx.SID, "connection", pctx.ConnectionName,
		"agent-name", pctx.AgentName, "mode", pctx.AgentMode, "ua", userAgent}
	log.With(logAttrs...).Infof("proxy connected: %v", stream)
	defer func() { log.With(logAttrs...).Infof("proxy disconnected, reason=%v", err) }()
	return s.listenClientMessages(stream)
}

func (s *Server) listenClientMessages(stream *streamclient.ProxyStream) error {
	pctx := stream.PluginContext()
	recvCh := grpc.NewStreamRecv(stream)
	for {
		var dstream *grpc.DataStream
		select {
		case <-stream.Context().Done():
			return stream.ContextCauseError()
		case <-pctx.Context.Done():
			return status.Error(codes.Aborted, "session ended, reached connection duration")
		case dstream = <-recvCh:
		}

		// receive data from stream channel
		pkt, err := dstream.Recv()
		if err != nil {
			if err == io.EOF {
				return err
			}
			if status, ok := status.FromError(err); ok && status.Code() == codes.Canceled {
				return err
			}
			log.Warnf("received error from client, err=%v", err)
			return status.Errorf(codes.Internal, "internal error, failed receiving client packet")
		}

		// do not process any system packets issued by the user
		if handled := handleSystemPacketRequests(pkt.Type); handled {
			continue
		}

		if pkt.Spec == nil {
			pkt.Spec = make(map[string][]byte)
		}
		pkt.Spec[pb.SpecGatewaySessionID] = []byte(pctx.SID)
		shouldProcessClientPacket := true
		connectResponse, err := stream.PluginExecOnReceive(pctx, pkt)
		switch v := err.(type) {
		case *plugintypes.InternalError:
			if v.HasInternalErr() {
				log.With("sid", pctx.SID).Errorf("plugin rejected packet, %v", v.FullErr())
			}
			return status.Errorf(codes.Internal, err.Error())
		case nil: // noop
		default:
			return status.Errorf(codes.Internal, err.Error())
		}

		// this function must deperecate the plugin system above
		if err := handleExtensionOnReceive(pctx, pkt); err != nil {
			log.With("sid", pctx.SID).Warn(err)
			return err
		}

		if connectResponse != nil {
			if connectResponse.Context != nil {
				pctx.Context = connectResponse.Context
			}
			if connectResponse.ClientPacket != nil {
				_ = stream.Send(connectResponse.ClientPacket)
				shouldProcessClientPacket = false
			}
		}
		if shouldProcessClientPacket {
			err = s.processClientPacket(stream, pkt, pctx)
			if err != nil {
				log.With("sid", pctx.SID, "agent-id", pctx.AgentID).Warnf("failed processing client packet, err=%v", err)
				return status.Errorf(codes.FailedPrecondition, err.Error())
			}
		}
	}
}

func handleExtensionOnReceive(pctx plugintypes.Context, pkt *pb.Packet) error {
	extContext := transportext.Context{
		OrgID:                               pctx.OrgID,
		SID:                                 pctx.SID,
		AgentID:                             pctx.AgentID,
		ConnectionName:                      pctx.ConnectionName,
		ConnectionType:                      pctx.ConnectionType,
		ConnectionSubType:                   pctx.ConnectionSubType,
		ConnectionEnvs:                      pctx.ConnectionSecret,
		ConnectionJiraTransitionNameOnClose: pctx.ConnectionJiraTransitionNameOnClose,
		ConnectionReviewers:                 pctx.ConnectionReviewers,
		UserEmail:                           pctx.UserEmail,
		Verb:                                pctx.ClientVerb,
	}

	return transportext.OnReceive(extContext, pkt)
}

func (s *Server) processClientPacket(stream *streamclient.ProxyStream, pkt *pb.Packet, pctx plugintypes.Context) error {
	switch pb.PacketType(pkt.Type) {
	case pbagent.SessionOpen:
		spec := map[string][]byte{
			pb.SpecGatewaySessionID: []byte(pctx.SID),
			pb.SpecConnectionType:   pb.ToConnectionType(pctx.ConnectionType, pctx.ConnectionSubType).Bytes(),
		}

		// The injection of these credentials via spec item are DEPRECATED in flavor of
		// propagating these values in the AgentConnectionParams
		// It should be kept for compatibility with older agents (< 1.35.11)
		if jsonCred := s.AppConfig.GcpDLPJsonCredentials(); jsonCred != "" {
			spec[pb.SpecAgentGCPRawCredentialsKey] = []byte(jsonCred)
		}

		if dlpProvider := s.AppConfig.DlpProvider(); dlpProvider != "" {
			spec[pb.SpecAgentDlpProvider] = []byte(dlpProvider)
		}

		if msPresidioAnalyzerURL := s.AppConfig.MSPresidioAnalyzerURL(); msPresidioAnalyzerURL != "" {
			spec[pb.SpecAgentMSPresidioAnalyzerURL] = []byte(msPresidioAnalyzerURL)
		}

		if msPresidioAnonymizerURL := s.AppConfig.MSPresidioAnomymizerURL(); msPresidioAnonymizerURL != "" {
			spec[pb.SpecAgentMSPresidioAnonymizerURL] = []byte(msPresidioAnonymizerURL)
		}

		if !stream.IsAgentOnline() {
			spec[pb.SpecClientExecArgsKey] = pkt.Spec[pb.SpecClientExecArgsKey]
			_ = stream.Send(&pb.Packet{
				Type: pbclient.SessionOpenAgentOffline,
				Spec: spec,
			})
			return pb.ErrAgentOffline
		}

		var entityTypesJsonData json.RawMessage
		if s.AppConfig.DlpProvider() == "mspresidio" {
			var err error
			entityTypesJsonData, err = models.GetDataMaskingEntityTypes(pctx.OrgID, pctx.ConnectionID)
			if err != nil {
				log.With("sid", pctx.SID, "connection-id", pctx.ConnectionID).Errorf("failed getting data masking entity types, err=%v", err)
				return status.Errorf(codes.Internal, "failed obtaining data masking entity types, err=%v", err)
			}
		}

		// override the entrypoint of the connection command
		if pctx.ClientVerb == pb.ClientVerbPlainExec {
			connectionCommandJSON, ok := pkt.Spec[pb.SpecConnectionCommand]
			if ok {
				var connectionCommand []string
				if err := json.Unmarshal(connectionCommandJSON, &connectionCommand); err != nil {
					log.With("sid", pctx.SID).Errorf("failed decoding connection command override, err=%v", err)
					return status.Errorf(codes.Internal, "failed decoding connection command override, err=%v", err)
				}
				pctx.ConnectionCommand = connectionCommand
			}
		}

		infoTypes := stream.GetRedactInfoTypes()
		if pctx.ClientVerb == pb.ClientVerbPlainExec {
			infoTypes = nil // do not redact info types for plain exec
			entityTypesJsonData = nil
		}

		var guardRailRulesJsonData json.RawMessage
		if pctx.ClientVerb != pb.ClientVerbPlainExec {
			connGuardRailRules, err := models.GetConnectionGuardRailRules(pctx.OrgID, pctx.ConnectionName)
			if err != nil {
				log.With("sid", pctx.SID, "connection", pctx.ConnectionName).Errorf("failed getting guard rail rules, err=%v", err)
				return status.Errorf(codes.Internal, "failed obtaining guard rail rules, err=%v", err)
			}

			if connGuardRailRules != nil {
				// Parse input rules
				var inputRules []guardrails.DataRules
				if connGuardRailRules.GuardRailInputRules != nil {
					inputRules, err = guardrails.Decode(connGuardRailRules.GuardRailInputRules)
					if err != nil {
						log.With("sid", pctx.SID, "connection", pctx.ConnectionName).Errorf("failed decoding guard rail input rules, err=%v", err)
						return status.Errorf(codes.Internal, "failed decoding guard rail input rules, err=%v", err)
					}
				}

				// Parse output rules
				var outputRules []guardrails.DataRules
				if connGuardRailRules.GuardRailOutputRules != nil {
					outputRules, err = guardrails.Decode(connGuardRailRules.GuardRailOutputRules)
					if err != nil {
						log.With("sid", pctx.SID, "connection", pctx.ConnectionName).Errorf("failed decoding guard rail output rules, err=%v", err)
						return status.Errorf(codes.Internal, "failed decoding guard rail output rules, err=%v", err)
					}
				}

				// Marshal both rules into a single json object
				guardRailRulesJsonData, err = json.Marshal(struct {
					InputRules  []guardrails.DataRules `json:"input_rules"`
					OutputRules []guardrails.DataRules `json:"output_rules"`
				}{
					InputRules:  inputRules,
					OutputRules: outputRules,
				})

				if err != nil {
					log.With("sid", pctx.SID, "connection", pctx.ConnectionName).Errorf("failed marshaling guard rail rules, err=%v", err)
					return status.Errorf(codes.Internal, "failed marshaling guard rail rules, err=%v", err)
				}
			}
		}

		clientArgs := clientArgsDecode(pkt.Spec)
		connParams, err := pb.GobEncode(&pb.AgentConnectionParams{
			ConnectionName:             pctx.ConnectionName,
			ConnectionType:             pb.ToConnectionType(pctx.ConnectionType, pctx.ConnectionSubType).String(),
			UserID:                     pctx.UserID,
			UserEmail:                  pctx.UserEmail,
			EnvVars:                    pctx.ConnectionSecret,
			CmdList:                    pctx.ConnectionCommand,
			ClientArgs:                 clientArgs,
			ClientVerb:                 pctx.ClientVerb,
			ClientOrigin:               pctx.ClientOrigin,
			DlpProvider:                s.AppConfig.DlpProvider(),
			DlpMode:                    s.AppConfig.DlpMode(),
			DlpGcpRawCredentialsJSON:   s.AppConfig.GcpDLPJsonCredentials(),
			DlpPresidioAnalyzerURL:     s.AppConfig.MSPresidioAnalyzerURL(),
			DlpPresidioAnonymizerURL:   s.AppConfig.MSPresidioAnomymizerURL(),
			DLPInfoTypes:               infoTypes,
			DataMaskingEntityTypesData: entityTypesJsonData,
			GuardRailRules:             guardRailRulesJsonData,
		})
		if err != nil {
			return fmt.Errorf("failed encoding connection params err=%v", err)
		}
		spec[pb.SpecAgentConnectionParamsKey] = connParams
		// Propagate client spec.
		// Do not allow replacing system ones
		for key, val := range pkt.Spec {
			if _, ok := spec[key]; ok {
				continue
			}
			spec[key] = val
		}

		err = stream.SendToAgent(&pb.Packet{Type: pbagent.SessionOpen, Spec: spec})
		log.With("sid", pctx.SID, "agent-name", pctx.AgentName).Infof("opening session with agent, sent=%v", err == nil)
		return err
	default:
		return stream.SendToAgent(pkt)
	}
}

func clientArgsDecode(spec map[string][]byte) []string {
	var clientArgs []string
	if spec != nil {
		encArgs := spec[pb.SpecClientExecArgsKey]
		if len(encArgs) > 0 {
			if err := pb.GobDecodeInto(encArgs, &clientArgs); err != nil {
				log.Printf("failed decoding args, err=%v", err)
			}
		}
	}
	return clientArgs
}

func handleSystemPacketRequests(pktType string) (handled bool) {
	if pktType == pbgateway.KeepAlive || strings.HasPrefix(pktType, "Sys") {
		handled = true
	}
	return
}

func (s *Server) ReleaseConnectionOnReview(orgID, sid, reviewOwnerSlackID, reviewStatus string) {
	if reviewStatus == string(openapi.ReviewStatusApproved) {
		pluginslack.SendApprovedMessage(
			orgID,
			reviewOwnerSlackID,
			sid,
			s.AppConfig.ApiURL(),
		)
	}

	proxyStream := streamclient.GetProxyStream(sid)
	if proxyStream != nil {
		var payload []byte
		packetType := pbclient.SessionOpenApproveOK
		if reviewStatus == string(openapi.ReviewStatusRejected) {
			packetType = pbclient.SessionClose
			payload = []byte(`access to connection has been denied`)
			proxyStream.Close(fmt.Errorf("access to connection has been denied"))
		}
		// TODO: return erroo to caller
		_ = proxyStream.Send(&pb.Packet{
			Type:    packetType,
			Spec:    map[string][]byte{pb.SpecGatewaySessionID: []byte(sid)},
			Payload: payload,
		})
	}
	log.With("sid", sid, "has-stream", proxyStream != nil).Infof("review status change")
}

func validateConnectionType(clientVerb string, pctx plugintypes.Context) error {
	if clientVerb == pb.ClientVerbExec {
		connType := pb.ToConnectionType(pctx.ConnectionType, pctx.ConnectionSubType)
		switch connType {
		case pb.ConnectionTypeTCP, pb.ConnectionTypeHttpProxy, pb.ConnectionTypeSSH:
			return status.Errorf(codes.InvalidArgument,
				fmt.Sprintf("exec is not allowed for %v type connections. Use 'hoop connect %s' instead", connType, pctx.ConnectionName))
		}
	}
	return nil
}
