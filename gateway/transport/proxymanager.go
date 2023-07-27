package transport

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	pbclient "github.com/runopsio/hoop/common/proto/client"
	pbgateway "github.com/runopsio/hoop/common/proto/gateway"
	"github.com/runopsio/hoop/gateway/analytics"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/clientstate"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
	"github.com/runopsio/hoop/gateway/user"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// proxyManager listen for API REST commands to manage grpc-client connections.
// The dispatcher functions are used to communicate directly with a channel performing
// actions directly to a stateful connection. It allows opening a session and
// disconnecting a client.
//
// In order for this to work properly, a grpc-client must be always connected, otherwise
// the API will fail to manage connections.
func (s *Server) proxyManager(stream pb.Transport_ConnectServer) error {
	ctx := stream.Context()
	md, _ := metadata.FromIncomingContext(ctx)
	clientOrigin := mdget(md, "origin")
	// TODO: validate origin

	var userCtx types.APIContext
	err := parseAuthContextInto(ctx, &userCtx)
	if err != nil {
		log.Error(err)
		return err
	}
	// sub, err := s.exchangeUserToken(token)
	// if err != nil {
	// 	log.Debugf("failed verifying access token, reason=%v", err)
	// 	return status.Errorf(codes.Unauthenticated, "invalid authentication")
	// }

	// userCtx, err := s.UserService.FindBySub(sub)
	// if err != nil || userCtx.User == nil {
	// 	return status.Errorf(codes.Unauthenticated, "invalid authentication")
	// }
	if err := stream.Send(&pb.Packet{Type: pbclient.ProxyManagerConnectOK}); err != nil {
		return err
	}
	log.Infof("proxymanager - client connected")
	storectx := storagev2.NewContext(userCtx.UserID, userCtx.OrgID, s.StoreV2).
		WithOrgName(userCtx.OrgName).
		WithUserInfo(
			userCtx.UserName,
			userCtx.UserEmail,
			string(userCtx.UserStatus),
			userCtx.UserGroups)
	sessionID := uuid.NewString()
	err = s.listenProxyManagerMessages(sessionID, storectx, stream)
	if status, ok := status.FromError(err); ok && status.Code() == codes.Canceled {
		log.With("origin", clientOrigin).Infof("grpc client connection canceled")
	}
	defer func() {
		s.disconnectClient(sessionID, err)
		_, _ = clientstate.Update(storectx, types.ClientStatusDisconnected)
		stateID, _ := uuid.NewRandomFromReader(bytes.NewBufferString(storectx.UserID))
		if len(stateID) > 0 {
			removeDispatcherState(stateID.String())
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

func (s *Server) listenProxyManagerMessages(sessionID string, ctx *storagev2.Context, stream pb.Transport_ConnectServer) error {
	var pctx plugintypes.Context
	streamCtx, cancelFn := context.WithCancel(stream.Context())
	recvCh := newDataStreamCh(stream, cancelFn)
	for {
		var dstream *dataStream
		select {
		case <-streamCtx.Done():
			return status.Errorf(codes.Canceled, "context done")
		case dstream = <-recvCh:
		}

		pkt := dstream.pkt
		if dstream.err != nil {
			if dstream.err == io.EOF {
				// log.With("session", pctx.SID).Debugf("EOF")
				return dstream.err
			}
			if status, ok := status.FromError(dstream.err); ok && status.Code() == codes.Canceled {
				return dstream.err
			}
			log.Warnf("received error from client, err=%v", dstream.err)
			return status.Errorf(codes.Internal, "internal error, failed receiving client packet")
		}

		if pkt.Spec == nil {
			pkt.Spec = make(map[string][]byte)
		}

		pkt.Spec[pb.SpecGatewaySessionID] = []byte(sessionID)

		switch pkt.Type {
		case pbgateway.KeepAlive: // noop
		case pbgateway.ProxyManagerConnectOKAck:
			md, _ := metadata.FromIncomingContext(streamCtx)
			newClient, err := clientstate.Update(ctx, types.ClientStatusReady,
				clientstate.WithOption("session", sessionID),
				clientstate.WithOption("version", mdget(md, "version")),
				clientstate.WithOption("go-version", mdget(md, "go-version")),
				clientstate.WithOption("platform", mdget(md, "platform")),
				clientstate.WithOption("hostname", mdget(md, "hostname")),
			)
			if err != nil {
				log.Errorf("failed client state to database, err=%v", err)
				return err
			}
			log.With("session", sessionID).Infof("proxymanager - client is ready: user=%v,hostname=%v,platform=%v,version=%v",
				ctx.UserEmail, mdget(md, "hostname"), mdget(md, "platform"), mdget(md, "version"))
			disp := newDispatcherState(cancelFn)
			addDispatcherStateEntry(newClient.ID, disp)
			select {
			case <-streamCtx.Done():
				return status.Errorf(codes.Canceled, "context canceled")
			case req := <-disp.requestCh:
				log.With("session", sessionID).Infof("starting connect phase for %s", req.RequestConnectionName)
				conn, err := s.ConnectionService.FindOne(storageCtxToUser(ctx), req.RequestConnectionName)
				if err != nil {
					sentry.CaptureException(err)
					disp.sendResponse(nil, err)
					return status.Errorf(codes.Internal, err.Error())
				}
				if conn == nil {
					disp.sendResponse(nil, status.Errorf(codes.NotFound, ""))
					return status.Errorf(codes.NotFound, fmt.Sprintf("connection '%v' not found", req.RequestConnectionName))
				}
				clientOrigin := pb.ConnectionOriginClientProxyManager
				pctx = plugintypes.Context{
					Context: context.Background(),
					SID:     sessionID,

					OrgID:      ctx.OrgID,
					UserID:     ctx.UserID,
					UserName:   ctx.UserName,
					UserEmail:  ctx.UserEmail,
					UserGroups: ctx.UserGroups,

					ConnectionID:      conn.Id,
					ConnectionName:    conn.Name,
					ConnectionType:    fmt.Sprintf("%v", conn.Type),
					ConnectionCommand: conn.Command,
					ConnectionSecret:  conn.Secret,
					ConnectionAgentID: conn.AgentId,

					ClientVerb:   pb.ClientVerbConnect,
					ClientOrigin: clientOrigin,

					ParamsData: map[string]any{},
				}
				if err := pctx.Validate(); err != nil {
					log.Errorf("failed validating plugin context, err=%v", err)
					sentry.CaptureException(err)
					disp.sendResponse(nil, err)
					return status.Errorf(codes.Internal,
						"failed validating connection context, contact the administrator")
				}

				s.startDisconnectClientSink(sessionID, clientOrigin, func(err error) {
					defer unbindClient(sessionID)
					if stream := getAgentStream(conn.AgentId); stream != nil {
						_ = stream.Send(&pb.Packet{
							Type: pbagent.SessionClose,
							Spec: map[string][]byte{
								pb.SpecGatewaySessionID: []byte(sessionID),
							},
						})
					}
					_ = s.pluginOnDisconnect(pctx, err)
				})

				// On Connect Phase Plugin
				plugins, err := s.loadConnectPlugins(ctx.APIContext, pctx)
				bindClient(sessionID, stream, plugins)
				if err != nil {
					disp.sendResponse(nil, err)
					return status.Errorf(codes.FailedPrecondition, err.Error())
				}

				s.Analytics.Track(ctx.APIContext, analytics.EventGrpcProxyManagerConnect, map[string]any{
					"session-id":      sessionID,
					"connection-name": req.RequestConnectionName,
					"connection-type": conn.Type,
					"client-version":  mdget(md, "version"),
					"go-version":      mdget(md, "go-version"),
					"platform":        mdget(md, "platform"),
					"hostname":        mdget(md, "hostname"),
					"user-agent":      mdget(md, "user-agent"),
					"origin":          clientOrigin,
					"verb":            pb.ClientVerbConnect,
				})

				log.With("session", sessionID).Infof("proxymanager - starting open session phase")
				onOpenSessionPkt := &pb.Packet{
					Type: pbagent.SessionOpen,
					Spec: map[string][]byte{
						pb.SpecJitTimeout:        []byte(req.RequestAccessDuration.String()),
						pb.SpecGatewaySessionID:  []byte(sessionID),
						pb.SpecClientRequestPort: []byte(req.RequestPort),
					},
				}
				connectResponse, err := s.pluginOnReceive(pctx, onOpenSessionPkt)
				if err != nil {
					disp.sendResponse(nil, err)
					return err
				}
				if connectResponse != nil {
					if connectResponse.Context != nil {
						streamCtx = connectResponse.Context
					}
					if cs := getClientStream(pctx.SID); cs != nil && connectResponse.ClientPacket != nil {
						_ = cs.Send(connectResponse.ClientPacket)
						disp.sendResponse(connectResponse.ClientPacket, nil)
						continue
					}
				}

				err = s.processSessionOpenPacket(onOpenSessionPkt, pctx)
				disp.sendResponse(nil, err)
				if err != nil {
					return err
				}
				log.With("session", sessionID).Info("proxymanager - session opened")
			case <-time.After(time.Hour * 12):
				log.Warnf("timeout (12h) waiting for api response")
			}
		default:
			connectResponse, err := s.pluginOnReceive(pctx, pkt)
			if err != nil {
				return err
			}
			if connectResponse != nil {
				if connectResponse.Context != nil {
					streamCtx = connectResponse.Context
				}
				if cs := getClientStream(pctx.SID); cs != nil && connectResponse.ClientPacket != nil {
					_ = cs.Send(connectResponse.ClientPacket)
					continue
				}
			}
			agentStream := getAgentStream(pctx.ConnectionAgentID)
			if agentStream == nil {
				return status.Errorf(codes.FailedPrecondition, fmt.Sprintf("agent not found for connection %s", pctx.ConnectionName))
			}
			_ = agentStream.Send(pkt)
		}
	}
}

func storageCtxToUser(ctx *storagev2.Context) *user.Context {
	return &user.Context{
		User: &user.User{
			Id:     ctx.UserID,
			Name:   ctx.UserName,
			Email:  ctx.UserEmail,
			Groups: ctx.UserGroups,
		}, Org: &user.Org{Id: ctx.OrgID}}
}
