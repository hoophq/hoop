package transport

import (
	"io"

	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/memory"
	pb "github.com/runopsio/hoop/common/proto"
	pbclient "github.com/runopsio/hoop/common/proto/client"
	pbgateway "github.com/runopsio/hoop/common/proto/gateway"
	"github.com/runopsio/hoop/gateway/storagev2/autoconnect"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"github.com/runopsio/hoop/gateway/user"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

var autoConnectStore = memory.New()

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
	return s.listenAutoConnectMessages(userCtx, stream)
}

func (s *Server) listenAutoConnectMessages(ctx *user.Context, stream pb.Transport_ConnectServer) error {
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

		switch pkt.Type {
		case pbgateway.ConnectOKAck:
			log.Infof("received ok-ack, saving to database")
			err := autoconnect.New(s.StoreV2).PutStatus(&types.UserContext{
				OrgID:  ctx.Org.Id,
				UserID: ctx.User.Id,
			}, "CONNECTED")
			if err != nil {
				log.Errorf("failed saving to database, err=%v", err)
				return err
			}
			// TODO: send pbclient.DoSubscribe
			// add transport sink function to propagate the api state
			// create a generic interface for allowing sending generic packets
			// autoConnectStore.Set()

			// save to database
		case pbgateway.Subscribe:

			// do subscribe
			log.Infof("do subscribe")
		}
	}
}
