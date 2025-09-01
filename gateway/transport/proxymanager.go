package transport

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/hoophq/hoop/common/apiutils"
	"github.com/hoophq/hoop/common/grpc"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	pbgateway "github.com/hoophq/hoop/common/proto/gateway"
	"github.com/hoophq/hoop/gateway/analytics"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2/clientstate"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	"github.com/hoophq/hoop/gateway/transport/streamclient"
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
	if err := stream.Send(&pb.Packet{Type: pbclient.ProxyManagerConnectOK}); err != nil {
		return err
	}
	err := s.listenProxyManagerMessages(stream)
	if status, ok := status.FromError(err); ok && status.Code() == codes.Canceled {
		log.Infof("grpc client connection canceled")
	}
	log.Infof("proxy manager disconnected, reason=%v", err)
	pluginCtx := stream.PluginContext()

	defer func() {
		_ = stream.Close(err)
		_, _ = clientstate.Update(pluginCtx, models.ProxyManagerStatusDisconnected)
		stateID := clientstate.DeterministicClientUUID(pluginCtx.GetUserID())
		if len(stateID) > 0 {
			removeDispatcherState(stateID)
		}
	}()
	switch v := err.(type) {
	case *plugintypes.InternalError:
		if v.HasInternalErr() {
			log.Errorf("plugin rejected packet, %v", v.FullErr())
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
			if err := s.proccessConnectOKAck(stream); err != nil {
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
			}
			if err = stream.SendToAgent(pkt); err != nil {
				return err
			}
		}
	}
}

func (s *Server) proccessConnectOKAck(stream *streamclient.ProxyStream) error {
	pctx := stream.PluginContext()
	newClient, err := clientstate.Update(pctx, models.ProxyManagerStatusReady,
		clientstate.WithOption("session", pctx.SID),
		clientstate.WithOption("version", stream.GetMeta("version")),
		clientstate.WithOption("go-version", stream.GetMeta("go-version")),
		clientstate.WithOption("platform", stream.GetMeta("platform")),
		clientstate.WithOption("hostname", stream.GetMeta("hostname")),
	)
	if err != nil {
		log.Errorf("failed updating proxy manager state, reason=%v", err)
		return err
	}

	logAttrs := []any{"connection", pctx.ConnectionName, "sid", pctx.SID,
		"mode", pctx.AgentMode, "agent-name", pctx.AgentName, "ua", stream.GetMeta("user-agent")}
	log.With(logAttrs...).Infof("proxy manager connected: %v", stream)
	// TODO: add reason to close?
	disp := newDispatcherState(func() { stream.Close(nil) })
	addDispatcherStateEntry(newClient.ID, disp)

	// wait up to 30 minutes and disconnect the proxy
	ctx, timeoutCancelFn := context.WithTimeout(context.Background(), time.Minute*30)
	defer timeoutCancelFn()
	select {
	case <-stream.Context().Done():
		return stream.ContextCauseError()
	case req := <-disp.requestCh:
		log.With("session", pctx.SID).Infof("starting connect phase for %s", req.RequestConnectionName)
		conn, err := models.GetConnectionByNameOrID(pctx, req.RequestConnectionName)
		if err != nil {
			log.Errorf("failed retrieving connection, reason=%v", err)
			disp.sendResponse(nil, err)
			return err
		}
		if conn == nil {
			disp.sendResponse(nil, status.Errorf(codes.NotFound, ""))
			return status.Errorf(codes.NotFound, fmt.Sprintf("connection '%v' not found", req.RequestConnectionName))
		}

		if conn.Type != "database" && conn.SubType.String != "tcp" {
			disp.sendResponse(nil, ErrUnsupportedType)
			return fmt.Errorf("connection type %s/%s not supported", conn.Type, conn.SubType.String)
		}

		if conn.AccessModeConnect == "disabled" {
			errorMessage := fmt.Sprintf("the %v connection has the access mode connect (Native) feature disabled", conn.Name)

			disp.sendResponse(nil, status.Error(codes.FailedPrecondition, errorMessage))
			return status.Error(codes.FailedPrecondition, errorMessage)
		}

		stream.SetPluginContext(func(pluginCtx *plugintypes.Context) {
			pluginCtx.ConnectionID = conn.ID
			pluginCtx.ConnectionName = conn.Name
			pluginCtx.ConnectionType = conn.Type
			pluginCtx.ConnectionSubType = conn.SubType.String
			pluginCtx.ConnectionCommand = conn.Command
			pluginCtx.ConnectionSecret = conn.AsSecrets()

			pluginCtx.AgentID = conn.AgentID.String
			pluginCtx.AgentMode = conn.AgentMode
			pluginCtx.AgentName = conn.AgentName
		})
		pctx = stream.PluginContext()
		if err := requestProxyConnection(stream); err != nil {
			return err
		}

		if err := stream.Save(); err != nil {
			disp.sendResponse(nil, err)
			return err
		}
		userAgent := apiutils.NormalizeUserAgent(func(key string) []string {
			return []string{stream.GetMeta("user-agent")}
		})
		analytics.New().Track(pctx.UserID, analytics.EventGrpcConnect, map[string]any{
			"connection-name":    req.RequestConnectionName,
			"connection-type":    conn.Type,
			"connection-subtype": conn.SubType,
			"client-version":     stream.GetMeta("version"),
			"platform":           stream.GetMeta("platform"),
			"user-agent":         userAgent,
			"verb":               pb.ClientVerbConnect,
		})

		log.With("session", pctx.SID).Infof("proxymanager - starting open session phase")
		onOpenSessionPkt := &pb.Packet{
			Type: pbagent.SessionOpen,
			Spec: map[string][]byte{
				pb.SpecJitTimeout:        fmt.Appendf(nil, "%vs", req.RequestAccessDurationSec),
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
		}

		err = s.processClientPacket(stream, onOpenSessionPkt, pctx)
		disp.sendResponse(nil, err)
		if err != nil {
			return err
		}
		log.With("session", pctx.SID).Info("proxymanager - session opened")
	case <-ctx.Done():
		return fmt.Errorf("timeout (30m) waiting for api response")
	}
	return nil
}
