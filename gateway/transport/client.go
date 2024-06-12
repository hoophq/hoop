package transport

import (
	"fmt"
	"io"

	"github.com/getsentry/sentry-go"
	"github.com/runopsio/hoop/common/apiutils"
	"github.com/runopsio/hoop/common/grpc"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	pbclient "github.com/runopsio/hoop/common/proto/client"
	pbgateway "github.com/runopsio/hoop/common/proto/gateway"
	"github.com/runopsio/hoop/gateway/analytics"
	pgplugins "github.com/runopsio/hoop/gateway/pgrest/plugins"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"github.com/runopsio/hoop/gateway/transport/connectionrequests"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
	"github.com/runopsio/hoop/gateway/transport/streamclient"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) subscribeClient(stream *streamclient.ProxyStream) (err error) {
	clientVerb := stream.GetMeta("verb")
	clientOrigin := stream.GetMeta("origin")
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
	defer func() { log.With(logAttrs...).Infof("proxy disconnected, err=%v", err) }()
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
				log.With("sid", pctx.SID).Debugf("EOF")
				return err
			}
			if status, ok := status.FromError(err); ok && status.Code() == codes.Canceled {
				return err
			}
			log.Warnf("received error from client, err=%v", err)
			sentry.CaptureException(err)
			return status.Errorf(codes.Internal, "internal error, failed receiving client packet")
		}
		// skip old/new clients
		if pkt.Type == pbgateway.KeepAlive || pkt.Type == "KeepAlive" {
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
	switch pb.PacketType(pkt.Type) {
	case pbagent.SessionOpen:
		spec := map[string][]byte{
			pb.SpecGatewaySessionID: []byte(pctx.SID),
			pb.SpecConnectionType:   pb.ToConnectionType(pctx.ConnectionType, pctx.ConnectionSubType).Bytes(),
		}

		if s.GcpDLPRawCredentials != "" {
			spec[pb.SpecAgentGCPRawCredentialsKey] = []byte(s.GcpDLPRawCredentials)
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
		connParams, err := s.addConnectionParams(clientArgs, stream.GetRedactInfoTypes(), pctx)
		if err != nil {
			return err
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
		return stream.SendToAgent(&pb.Packet{Type: pbagent.SessionOpen, Spec: spec})
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

func (s *Server) addConnectionParams(clientArgs, infoTypes []string, pctx plugintypes.Context) ([]byte, error) {
	plugins, err := pgplugins.New().FetchAll(pctx)
	if err != nil {
		return nil, fmt.Errorf("failed loading plugin hooks, err=%v", err)
	}
	var pluginHookList []map[string]any
	for _, pl := range plugins {
		if pl.Source == nil {
			continue
		}
		var pluginName string
		for _, conn := range pl.Connections {
			if pctx.ConnectionName == conn.Name {
				pluginName = pl.Name
				break
			}
		}
		if pluginName == "" {
			continue
		}

		for _, plConn := range pl.Connections {
			if plConn.Name != pctx.ConnectionName {
				continue
			}
			// TODO: connection config should change in the future to accept
			// a map instead of a list. For now, the first record is used
			// as the configuration encoded as base64 + json
			var connectionConfigB64JSONEnc string
			if len(plConn.Config) > 0 {
				connectionConfigB64JSONEnc = plConn.Config[0]
			}
			var pluginEnvVars map[string]string
			if pl.Config != nil {
				pluginEnvVars = pl.Config.EnvVars
			}
			pluginHookList = append(pluginHookList, map[string]any{
				"plugin_registry":   s.PluginRegistryURL,
				"plugin_name":       pl.Name,
				"plugin_source":     *pl.Source,
				"plugin_envvars":    pluginEnvVars,
				"connection_config": map[string]any{"jsonb64": connectionConfigB64JSONEnc},
			})
			// load the plugin once per connection
			break
		}
	}
	encConnectionParams, err := pb.GobEncode(&pb.AgentConnectionParams{
		ConnectionName: pctx.ConnectionName,
		ConnectionType: pb.ToConnectionType(pctx.ConnectionType, pctx.ConnectionSubType).String(),
		UserID:         pctx.UserID,
		UserEmail:      pctx.UserEmail,
		EnvVars:        pctx.ConnectionSecret,
		CmdList:        pctx.ConnectionCommand,
		ClientArgs:     clientArgs,
		ClientVerb:     pctx.ClientVerb,
		ClientOrigin:   pctx.ClientOrigin,
		DLPInfoTypes:   infoTypes,
		PluginHookList: pluginHookList,
	})
	if err != nil {
		return nil, fmt.Errorf("failed encoding connection params err=%v", err)
	}

	return encConnectionParams, nil
}

func (s *Server) ReviewStatusChange(rev *types.Review) {
	if proxyStream := streamclient.GetProxyStream(rev.Session); proxyStream != nil {
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
}
