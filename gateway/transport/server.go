package transport

import (
	"github.com/runopsio/hoop/gateway/agent"
	"github.com/runopsio/hoop/gateway/connection"
	"github.com/runopsio/hoop/gateway/user"
	pb "github.com/runopsio/hoop/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"log"
	"net"
	"strings"
)

type (
	Server struct {
		pb.UnimplementedTransportServer
		AgentService      agent.Service
		ConnectionService connection.Service
		UserService       user.Service
	}
)

const (
	agentOrigin  = "agent"
	clientOrigin = "client"
)

func (s *Server) StartRPCServer() {
	log.Println("Starting gRPC server...")

	listener, err := net.Listen("tcp", ":9090")
	if err != nil {
		panic(err)
	}

	svr := grpc.NewServer()
	pb.RegisterTransportServer(svr, s)
	if err := svr.Serve(listener); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

func (s *Server) Connect(stream pb.Transport_ConnectServer) error {
	log.Println("starting new grpc connection...")
	ctx := stream.Context()

	md, _ := metadata.FromIncomingContext(ctx)
	t := md.Get("authorization")
	if len(t) == 0 {
		return status.Errorf(codes.Unauthenticated, "invalid authentication")
	}

	tokenValue := t[0]
	tokenParts := strings.Split(tokenValue, " ")
	if len(tokenParts) != 2 || tokenParts[0] != "Bearer" || tokenParts[1] == "" {
		return status.Errorf(codes.Unauthenticated, "invalid authentication")
	}

	token := tokenParts[1]

	origin := clientOrigin
	if strings.HasPrefix(token, "x-agt") {
		origin = agentOrigin
	}

	if origin == agentOrigin {
		err := s.subscribeAgent(stream, token)
		if err != nil {
			return err
		}
	}

	return nil
}
