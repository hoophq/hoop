package transport

import (
	"github.com/runopsio/hoop/gateway/plugin"
	"log"
	"net"
	"strings"

	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/agent"
	"github.com/runopsio/hoop/gateway/client"
	"github.com/runopsio/hoop/gateway/connection"
	"github.com/runopsio/hoop/gateway/user"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type (
	Server struct {
		pb.UnimplementedTransportServer
		AgentService      agent.Service
		ClientService     client.Service
		ConnectionService connection.Service
		UserService       user.Service
		PluginService     plugin.Service
	}
)

const (
	agentOrigin  = "agent"
	clientOrigin = "client"
	listenAddr   = "0.0.0.0:8010"
)

func (s *Server) StartRPCServer() {
	log.Printf("starting gateway at %v", listenAddr)
	listener, err := net.Listen("tcp", listenAddr)
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
		return s.subscribeAgent(stream, token)
	}
	return s.subscribeClient(stream, token)
}

func extractData(md metadata.MD, metaName string) string {
	data := md.Get(metaName)
	if len(data) == 0 {
		return ""
	}

	return data[0]
}
