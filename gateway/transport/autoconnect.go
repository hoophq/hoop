package transport

import (
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
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/autoconnect"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
	"github.com/runopsio/hoop/gateway/user"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type sharedDispatcher struct {
	apiResponseCh       chan string
	subscribeResponseCh chan error
}

func (s *Server) autoConnect(stream pb.Transport_ConnectServer, token string) error {
	ctx := stream.Context()
	md, _ := metadata.FromIncomingContext(ctx)
	_ = md

	// hostname := extractData(md, "hostname")
	// clientVerb := extractData(md, "verb")
	// clientOrigin := extractData(md, "origin")

	sub, err := s.exchangeUserToken(token)
	if err != nil {
		log.Debugf("failed verifying access token, reason=%v", err)
		return status.Errorf(codes.Unauthenticated, "invalid authentication")
	}

	userCtx, err := s.UserService.FindBySub(sub)
	if err != nil || userCtx.User == nil {
		return status.Errorf(codes.Unauthenticated, "invalid authentication")
	}
	if err := stream.Send(&pb.Packet{Type: pbclient.ConnectOK}); err != nil {
		return err
	}
	log.Infof("autoconnect client connected!")
	storectx := storagev2.NewContext(userCtx.User.Id, userCtx.Org.Id, s.StoreV2)
	err = s.listenAutoConnectMessages(storectx, stream)
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

func (s *Server) listenAutoConnectMessages(ctx *storagev2.Context, stream pb.Transport_ConnectServer) error {
	var pctx plugintypes.Context
	for {
		select {
		case <-stream.Context().Done():
			return nil
		default:
		}

		// receive data from stream
		pkt, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				// log.With("session", pctx.SID).Debugf("EOF")
				return err
			}
			if status, ok := status.FromError(err); ok && status.Code() == codes.Canceled {
				return err
			}
			log.Warnf("received error from client, err=%v", err)
			return status.Errorf(codes.Internal, "internal error, failed receiving client packet")
		}

		if pkt.Spec == nil {
			pkt.Spec = make(map[string][]byte)
		}

		if pctx.SID != "" {
			pkt.Spec[pb.SpecGatewaySessionID] = []byte(pctx.SID)
		}

		switch pkt.Type {
		case pbgateway.KeepAlive: // noop
		case pbgateway.ConnectOKAck:
			log.Infof("received ok-ack, saving to database")
			ac, err := autoconnect.PutStatus(ctx, "CONNECTED")
			if err != nil {
				log.Errorf("failed saving to database, err=%v", err)
				return err
			}
			disp := &sharedDispatcher{make(chan string), make(chan error)}
			addStateEntry(autoConnectState, ac.ID, disp)
			log.Infof("waiting for api to send commands")
			select {
			case connectionName := <-disp.apiResponseCh:
				log.Infof("starting subscribe for %s", connectionName)
				// TODO: delete state entry
				// - subscribe
				conn, err := s.ConnectionService.FindOne(storageCtxToUser(ctx), connectionName)
				if err != nil {
					sentry.CaptureException(err)
					return status.Errorf(codes.Internal, err.Error())
				}
				if conn == nil {
					return status.Errorf(codes.NotFound, fmt.Sprintf("connection '%v' not found", connectionName))
				}
				// subscribe
				sessionID := uuid.NewString()
				clientOrigin := pb.ConnectionOriginClientAutoConnect
				pctx = plugintypes.Context{
					Context: context.Background(),
					SID:     sessionID,

					// OrgID:      userCtx.Org.Id,
					// UserID:     userCtx.User.Id,
					// UserName:   userCtx.User.Name,
					// UserEmail:  userCtx.User.Email,
					// UserGroups: userCtx.User.Groups,

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
					return status.Errorf(codes.Internal,
						"failed validating connection context, contact the administrator")
				}

				s.startDisconnectClientSink(sessionID, clientOrigin, func(err error) {
					defer unbindClient(sessionID)
					// c.Status = client.StatusDisconnected
					// _, _ = s.ClientService.Persist(c)
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
				plugins, err := s.loadConnectPlugins(storageCtxToUser(ctx), pctx)
				bindClient(sessionID, stream, plugins)
				if err != nil {
					s.disconnectClient(sessionID, err)
					s.trackSessionStatus(sessionID, pb.SessionPhaseClientErr, err)
					return status.Errorf(codes.FailedPrecondition, err.Error())
				}

				// s.Analytics.Track(ctx.UserID, pb.ClientVerbConnect, map[string]any{
				// 	"sessionID":       sessionID,
				// 	"connection-name": connectionName,
				// 	"connection-type": conn.Type,
				// 	"hostname":        hostname,
				// 	"client-version":  c.Version,
				// 	"platform":        c.Platform,
				// })

				// log.With("session", sessionID).
				// 	Infof("proxy subscribed: user=%v,hostname=%v,origin=%v,verb=%v,platform=%v,version=%v,goversion=%v",
				// 		ctx.UserEmail, c.Hostname, clientOrigin, c.Verb, c.Platform, c.Version, c.GoVersion)
				// err = subscribeAutoConnect(pctx, ctx)

				// On Open Session Phase
				onOpenSessionPkt := &pb.Packet{
					Type: pbagent.SessionOpen,
					Spec: map[string][]byte{
						pb.SpecJitTimeout: []byte(`30m`),
					},
				}
				connectResponse, err := s.pluginOnReceive(pctx, onOpenSessionPkt)
				if err != nil {
					return err
				}
				if connectResponse != nil {
					if connectResponse.Context != nil {
						pctx.Context = connectResponse.Context
					}
					if cs := getClientStream(pctx.SID); cs != nil && connectResponse.ClientPacket != nil {
						_ = cs.Send(connectResponse.ClientPacket)
						continue
					}
				}
				err = s.processSessionOpenPacket(onOpenSessionPkt, pctx)
				select {
				case disp.subscribeResponseCh <- err:
				case <-time.After(time.Second * 2): // hang if doesn't send in 2 seconds
					log.Warnf("timeout (2s) sending response back to api client")
				}
				// disconnect
				if err != nil {
					return err
				}
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
					pctx.Context = connectResponse.Context
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
	return &user.Context{User: &user.User{Id: ctx.UserID}, Org: &user.Org{Id: ctx.OrgID}}
}
