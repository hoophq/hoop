package transport

import (
	"fmt"
	"io"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/runopsio/hoop/common/apiutils"
	"github.com/runopsio/hoop/common/grpc"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	pbclient "github.com/runopsio/hoop/common/proto/client"
	pbgateway "github.com/runopsio/hoop/common/proto/gateway"
	"github.com/runopsio/hoop/gateway/analytics"
	apiconnections "github.com/runopsio/hoop/gateway/api/connections"
	"github.com/runopsio/hoop/gateway/storagev2/clientstate"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
	"github.com/runopsio/hoop/gateway/transport/streamclient"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// type proxyManagerContext

// proxyManager listen for API REST commands to manage grpc-client connections.
// The dispatcher functions are used to communicate directly with a channel performing
// actions directly to a stateful connection. It allows opening a session and
// disconnecting a client.
//
// In order for this to work properly, a grpc-client must be always connected, otherwise
// the API will fail to manage connections.
func (s *Server) proxyManager(stream *streamclient.ProxyStream) error {
	// ctx := stream.Context()
	// md, _ := metadata.FromIncomingContext(ctx)
	// clientOrigin := mdget(md, "origin")
	// TODO: validate origin

	// var gwctx authinterceptor.GatewayContext
	// err := authinterceptor.ParseGatewayContextInto(ctx, &gwctx)
	// if err != nil {
	// 	log.Error(err)
	// 	return err
	// }
	// userCtx := gwctx.UserContext
	if err := stream.Send(&pb.Packet{Type: pbclient.ProxyManagerConnectOK}); err != nil {
		return err
	}
	log.Infof("proxymanager - client connected")
	// storectx := storagev2.NewContext(userCtx.UserID, userCtx.OrgID, s.StoreV2).
	// 	WithOrgName(userCtx.OrgName).
	// 	WithUserInfo(
	// 		userCtx.UserName, userCtx.UserEmail, string(userCtx.UserStatus),
	// 		userCtx.UserPicture, userCtx.UserGroups)
	err := s.listenProxyManagerMessages(stream)
	if status, ok := status.FromError(err); ok && status.Code() == codes.Canceled {
		log.Infof("grpc client connection canceled")
	}
	log.Infof("proxy manager disconnected, reason=%v", err)
	pluginCtx := stream.PluginContext()

	defer func() {
		_ = stream.Close(err)
		// DisconnectClient(sessionID, err)
		_, _ = clientstate.Update(pluginCtx, types.ClientStatusDisconnected)
		stateID := clientstate.DeterministicClientUUID(pluginCtx.GetUserID())
		if len(stateID) > 0 {
			removeDispatcherState(stateID)
		}
	}()
	switch v := err.(type) {
	case *plugintypes.InternalError:
		if v.HasInternalErr() {
			log.Errorf("plugin rejected packet, %v", v.FullErr())
			sentry.CaptureException(fmt.Errorf(v.FullErr()))
		}
		return status.Errorf(codes.Internal, err.Error())
	}
	return err
}

func (s *Server) listenProxyManagerMessages(stream *streamclient.ProxyStream) error {
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

		pkt, err := dstream.Recv()
		if err != nil {
			if err == io.EOF {
				return err
			}
			if status, ok := status.FromError(err); ok && status.Code() == codes.Canceled {
				return err
			}
			log.Warnf("received error from proxy manager client, err=%v", err)
			sentry.CaptureException(err)
			return status.Errorf(codes.Internal, "internal error, failed receiving client packet")
		}

		if pkt.Spec == nil {
			pkt.Spec = make(map[string][]byte)
		}

		pkt.Spec[pb.SpecGatewaySessionID] = []byte(pctx.SID)
		switch pkt.Type {
		case pbgateway.KeepAlive: // noop
		case pbgateway.ProxyManagerConnectOKAck:
			if err := s.processInitProxyManager(stream); err != nil {
				return err
			}
		default:
			connectResponse, err := stream.PluginExecOnReceive(pctx, pkt)
			if err != nil {
				return err
			}
			if connectResponse != nil {
				if connectResponse.Context != nil {
					pctx.Context = connectResponse.Context
				}
				if connectResponse.ClientPacket != nil {
					_ = stream.Send(connectResponse.ClientPacket)
					continue
				}

				// if cs := getClientStream(pctx.SID); cs != nil && connectResponse.ClientPacket != nil {
				// 	_ = cs.Send(connectResponse.ClientPacket)
				// 	continue
				// }
			}
			if err = stream.SendToAgent(pkt); err != nil {
				return err
			}
			// agentStream := getAgentStream(pctx.ConnectionAgentID)
			// if agentStream == nil {
			// 	return status.Errorf(codes.FailedPrecondition, fmt.Sprintf("agent not found for connection %s", pctx.ConnectionName))
			// }
			// _ = agentStream.Send(pkt)
		}
	}
}

func (s *Server) processInitProxyManager(stream *streamclient.ProxyStream) error {
	// TODO: temove time.After
	pctx := stream.PluginContext()
	newClient, err := clientstate.Update(pctx, types.ClientStatusReady,
		clientstate.WithOption("session", pctx.SID),
		clientstate.WithOption("version", stream.GetMeta("version")),
		clientstate.WithOption("go-version", stream.GetMeta("go-version")),
		clientstate.WithOption("platform", stream.GetMeta("platform")),
		clientstate.WithOption("hostname", stream.GetMeta("hostname")),
	)
	if err != nil {
		log.Errorf("failed client state to database, err=%v", err)
		return err
	}

	logAttrs := []any{"connection", pctx.ConnectionName, "sid", pctx.SID,
		"mode", pctx.AgentMode, "agent-name", pctx.AgentName, "ua", stream.GetMeta("user-agent")}
	log.With(logAttrs...).Infof("proxy manager connected: %v", stream)
	// TODO: add reason to close?
	disp := newDispatcherState(func() { stream.Close(nil) })
	addDispatcherStateEntry(newClient.ID, disp)
	select {
	case <-stream.Context().Done():
		return stream.ContextCauseError()
	case req := <-disp.requestCh:
		log.With("session", pctx.SID).Infof("starting connect phase for %s", req.RequestConnectionName)
		conn, err := apiconnections.FetchByName(pctx, req.RequestConnectionName)
		if err != nil {
			log.Errorf("failed retrieving connection, reason=%v", err)
			disp.sendResponse(nil, err)
			return err
		}
		if conn == nil {
			disp.sendResponse(nil, status.Errorf(codes.NotFound, ""))
			return status.Errorf(codes.NotFound, fmt.Sprintf("connection '%v' not found", req.RequestConnectionName))
		}

		if conn.Type != "database" && conn.SubType != "tcp" {
			disp.sendResponse(nil, ErrUnsupportedType)
			return fmt.Errorf("connection type %s/%s not supported", conn.Type, conn.SubType)
		}
		clientOrigin := pb.ConnectionOriginClientProxyManager
		stream.SetPluginContext(func(pluginCtx *plugintypes.Context) {
			pluginCtx.ConnectionID = conn.ID
			pluginCtx.ConnectionName = conn.Name
			pluginCtx.ConnectionType = conn.Type
			pluginCtx.ConnectionSubType = conn.SubType
			pluginCtx.ConnectionCommand = conn.Command
			pluginCtx.ConnectionSecret = conn.AsSecrets()

			pluginCtx.AgentID = conn.AgentID
			pluginCtx.AgentMode = conn.Agent.Mode
			pluginCtx.AgentName = conn.Agent.Name
		})
		pctx = stream.PluginContext()
		// defer func() { stream.Close(err) }()
		// pctx = plugintypes.Context{
		// 	Context: context.Background(),
		// 	SID:     sessionID,

		// 	OrgID:      ctx.OrgID,
		// 	UserID:     ctx.UserID,
		// 	UserName:   ctx.UserName,
		// 	UserEmail:  ctx.UserEmail,
		// 	UserGroups: ctx.UserGroups,

		// 	ConnectionID:      conn.ID,
		// 	ConnectionName:    conn.Name,
		// 	ConnectionType:    conn.Type,
		// 	ConnectionSubType: conn.SubType,
		// 	ConnectionCommand: conn.CmdEntrypoint,
		// 	ConnectionSecret:  conn.Secrets,

		// 	AgentID: conn.AgentID,

		// 	ClientVerb:   pb.ClientVerbConnect,
		// 	ClientOrigin: clientOrigin,

		// 	ParamsData: map[string]any{},
		// }
		// if err := pctx.Validate(); err != nil {
		// 	log.Errorf("failed validating plugin context, err=%v", err)
		// 	sentry.CaptureException(err)
		// 	disp.sendResponse(nil, err)
		// 	return status.Errorf(codes.Internal,
		// 		"failed validating connection context, contact the administrator")
		// }

		// s.startDisconnectClientSink(sessionID, clientOrigin, func(err error) {
		// 	defer unbindClient(sessionID)
		// 	if stream := getAgentStream(conn.AgentID); stream != nil {
		// 		_ = stream.Send(&pb.Packet{
		// 			Type: pbagent.SessionClose,
		// 			Spec: map[string][]byte{
		// 				pb.SpecGatewaySessionID: []byte(sessionID),
		// 			},
		// 		})
		// 	}
		// 	_ = s.pluginOnDisconnect(pctx, err)
		// })

		// On Connect Phase Plugin
		// plugins, err := s.loadConnectPlugins(pgrest.NewOrgContext(ctx.OrgID), pctx)
		// bindClient(sessionID, stream, plugins)
		// if err != nil {
		// 	disp.sendResponse(nil, err)
		// 	return status.Errorf(codes.FailedPrecondition, err.Error())
		// }
		if err := stream.Save(); err != nil {
			disp.sendResponse(nil, err)
			return err
		}
		userAgent := apiutils.NormalizeUserAgent(func(key string) []string {
			return []string{stream.GetMeta("user-agent")}
		})
		analytics.New().Track(pctx.UserEmail, analytics.EventGrpcConnect, map[string]any{
			"connection-name":    req.RequestConnectionName,
			"connection-type":    conn.Type,
			"connection-subtype": conn.SubType,
			"client-version":     stream.GetMeta("version"),
			"platform":           stream.GetMeta("platform"),
			"hostname":           stream.GetMeta("hostname"),
			"user-agent":         userAgent,
			"origin":             clientOrigin,
			"verb":               pb.ClientVerbConnect,
		})

		log.With("session", pctx.SID).Infof("proxymanager - starting open session phase")
		onOpenSessionPkt := &pb.Packet{
			Type: pbagent.SessionOpen,
			Spec: map[string][]byte{
				pb.SpecJitTimeout:        []byte(req.RequestAccessDuration.String()),
				pb.SpecGatewaySessionID:  []byte(pctx.SID),
				pb.SpecClientRequestPort: []byte(req.RequestPort),
			},
		}
		connectResponse, err := stream.PluginExecOnReceive(pctx, onOpenSessionPkt)
		if err != nil {
			disp.sendResponse(nil, err)
			return err
		}
		if connectResponse != nil {
			if connectResponse.Context != nil {
				pctx.Context = connectResponse.Context
			}
			if connectResponse.ClientPacket != nil {
				_ = stream.Send(connectResponse.ClientPacket)
				disp.sendResponse(connectResponse.ClientPacket, nil)
				return nil
			}
			// if cs := getClientStream(pctx.SID); cs != nil && connectResponse.ClientPacket != nil {
			// 	_ = cs.Send(connectResponse.ClientPacket)
			// 	disp.sendResponse(connectResponse.ClientPacket, nil)
			// 	continue
			// }
		}

		// TODO: change stream to interface here!
		err = s.processClientPacket(stream, onOpenSessionPkt, pctx)
		disp.sendResponse(nil, err)
		if err != nil {
			return err
		}
		log.With("session", pctx.SID).Info("proxymanager - session opened")
	case <-time.After(time.Hour * 12):
		log.Warnf("timeout (12h) waiting for api response")
	}
	return nil
}
