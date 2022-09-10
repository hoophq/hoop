package transport

import (
	pb "github.com/runopsio/hoop/domain/proto"
	"github.com/runopsio/hoop/gateway/agent"
	"github.com/runopsio/hoop/gateway/connection"
	"github.com/runopsio/hoop/gateway/user"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
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
	token := md.Get("authorization")[0]

	origin := clientOrigin
	if strings.HasPrefix(token, "x-agt") {
		origin = agentOrigin
	}

	if origin == agentOrigin {
		err := s.subscribeAgent(stream)
		if err != nil {
			return err
		}
	}

	return nil
}
