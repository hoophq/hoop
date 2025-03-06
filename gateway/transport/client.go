package transport

import (
	"fmt"
	"io"

	"github.com/getsentry/sentry-go"
	"github.com/hoophq/hoop/common/apiutils"
	"github.com/hoophq/hoop/common/grpc"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	pbgateway "github.com/hoophq/hoop/common/proto/gateway"
	pbsys "github.com/hoophq/hoop/common/proto/sys"
	"github.com/hoophq/hoop/gateway/analytics"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/storagev2/types"
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

	connType := pb.ToConnectionType(pctx.ConnectionType, pctx.ConnectionSubType)
	if connType == pb.ConnectionTypeTCP && clientVerb == pb.ClientVerbExec {
		return status.Errorf(codes.InvalidArgument,
			fmt.Sprintf("exec is not allowed for tcp type connections. Use 'hoop connect %s' instead", pctx.ConnectionName))
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
	analytics.New().Track(pctx.UserEmail, eventName, map[string]any{
		"connection-name":       pctx.ConnectionName,
		"connection-type":       pctx.ConnectionType,
		"connection-subtype":    pctx.ConnectionSubType,
		"connection-entrypoint": cmdEntrypoint,
		"client-version":        stream.GetMeta("version"),
		"platform":              stream.GetMeta("platform"),
		"hostname":              stream.GetMeta("hostname"),
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

		// Do not let clients send system packets
		if pkt.Type == pbgateway.KeepAlive || pkt.Type == pbsys.ProvisionDBRolesRequest {
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
				sentry.CaptureException(fmt.Errorf(v.FullErr()))
			}
			return status.Errorf(codes.Internal, err.Error())
		case nil: // noop
		default:
			return status.Errorf(codes.Internal, err.Error())
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

func (s *Server) processClientPacket(stream *streamclient.ProxyStream, pkt *pb.Packet, pctx plugintypes.Context) error {
	extContext := transportext.Context{
		OrgID:                               pctx.OrgID,
		SID:                                 pctx.SID,
		ConnectionName:                      pctx.ConnectionName,
		ConnectionJiraTransitionNameOnClose: pctx.ConnectionJiraTransitionNameOnClose,
		Verb:                                pctx.ClientVerb,
	}

	if err := transportext.OnReceive(extContext, pkt); err != nil {
		log.With("sid", pctx.SID).Error(err)
		return err
	}

	switch pb.PacketType(pkt.Type) {
	case pbagent.SessionOpen:
		spec := map[string][]byte{
			pb.SpecGatewaySessionID: []byte(pctx.SID),
			pb.SpecConnectionType:   pb.ToConnectionType(pctx.ConnectionType, pctx.ConnectionSubType).Bytes(),
		}

		if jsonCred := appconfig.Get().GcpDLPJsonCredentials(); jsonCred != "" {
			spec[pb.SpecAgentGCPRawCredentialsKey] = []byte(jsonCred)
		}

		if dlpProvider := appconfig.Get().DlpProvider(); dlpProvider != "" {
			spec[pb.SpecAgentDlpProvider] = []byte(dlpProvider)
		}

		if msPresidioAnalyzerURL := appconfig.Get().MSPresidioAnalyzerURL(); msPresidioAnalyzerURL != "" {
			spec[pb.SpecAgentMSPresidioAnalyzerURL] = []byte(msPresidioAnalyzerURL)
		}

		if msPresidioAnonymizerURL := appconfig.Get().MSPresidioAnomymizerURL(); msPresidioAnonymizerURL != "" {
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
		clientArgs := clientArgsDecode(pkt.Spec)
		connParams, err := pb.GobEncode(&pb.AgentConnectionParams{
			ConnectionName: pctx.ConnectionName,
			ConnectionType: pb.ToConnectionType(pctx.ConnectionType, pctx.ConnectionSubType).String(),
			UserID:         pctx.UserID,
			UserEmail:      pctx.UserEmail,
			EnvVars:        pctx.ConnectionSecret,
			CmdList:        pctx.ConnectionCommand,
			ClientArgs:     clientArgs,
			ClientVerb:     pctx.ClientVerb,
			ClientOrigin:   pctx.ClientOrigin,
			DLPInfoTypes:   stream.GetRedactInfoTypes(),
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

func (s *Server) ReviewStatusChange(rev *types.Review) {
	if rev.Status == types.ReviewStatusApproved {
		pluginslack.SendApprovedMessage(
			rev.OrgId,
			rev.ReviewOwner.SlackID,
			rev.Session,
			s.IDProvider.ApiURL,
		)
	}

	proxyStream := streamclient.GetProxyStream(rev.Session)
	if proxyStream != nil {
		payload := []byte(rev.Input)
		packetType := pbclient.SessionOpenApproveOK
		if rev.Status == types.ReviewStatusRejected {
			packetType = pbclient.SessionClose
			payload = []byte(`access to connection has been denied`)
			proxyStream.Close(fmt.Errorf("access to connection has been denied"))
		}
		// TODO: return erroo to caller
		_ = proxyStream.Send(&pb.Packet{
			Type:    packetType,
			Spec:    map[string][]byte{pb.SpecGatewaySessionID: []byte(rev.Session)},
			Payload: payload,
		})
	}
	log.With("sid", rev.Session, "connection", rev.Connection.Name, "has-stream", proxyStream != nil).
		Infof("review status change")
}
