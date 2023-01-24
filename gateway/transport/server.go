package transport

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strings"

	"github.com/runopsio/hoop/gateway/notification"
	"github.com/runopsio/hoop/gateway/review"
	"github.com/runopsio/hoop/gateway/review/jit"

	"github.com/runopsio/hoop/gateway/plugin"
	"github.com/runopsio/hoop/gateway/security/idp"
	"github.com/runopsio/hoop/gateway/session"

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
		AgentService         agent.Service
		ClientService        client.Service
		ConnectionService    connection.Service
		UserService          user.Service
		PluginService        plugin.Service
		SessionService       session.Service
		ReviewService        review.Service
		JitService           jit.Service
		NotificationService  notification.Service
		IDProvider           *idp.Provider
		Profile              string
		GcpDLPRawCredentials string
		PluginRegistryURL    string
		Analytics            user.Analytics
	}

	AnalyticsService interface {
		Track(userID, eventName string, properties map[string]any)
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
		log.Fatal(err)
	}

	if err := s.ValidateConfiguration(); err != nil {
		log.Fatal(err)
	}

	svr := grpc.NewServer()
	pb.RegisterTransportServer(svr, s)
	if err := svr.Serve(listener); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

func (s *Server) Connect(stream pb.Transport_ConnectServer) error {
	ctx := stream.Context()
	var token string
	md, _ := metadata.FromIncomingContext(ctx)
	o := md.Get("origin")
	if len(o) == 0 {
		log.Printf("client missing origin")
		return status.Error(codes.InvalidArgument, "missing origin")
	}

	origin := o[0]

	if s.Profile == pb.DevProfile {
		token = "x-hooper-test-token"
		if origin == pb.ConnectionOriginAgent {
			token = "x-agt-test-token"
		}
	} else {
		t := md.Get("authorization")
		if len(t) == 0 {
			return status.Error(codes.Unauthenticated, "invalid authentication")
		}

		tokenValue := t[0]
		tokenParts := strings.Split(tokenValue, " ")
		if len(tokenParts) != 2 || tokenParts[0] != "Bearer" || tokenParts[1] == "" {
			return status.Error(codes.Unauthenticated, "invalid authentication")
		}

		token = tokenParts[1]
	}

	if origin == pb.ConnectionOriginAgent {
		return s.subscribeAgent(stream, token)
	}
	return s.subscribeClient(stream, token)
}

func (s *Server) ValidateConfiguration() error {
	var js json.RawMessage
	if s.GcpDLPRawCredentials == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(s.GcpDLPRawCredentials), &js); err != nil {
		return fmt.Errorf("failed to parse env GOOGLE_APPLICATION_CREDENTIALS_JSON, it should be a valid JSON")
	}
	return nil
}

func extractData(md metadata.MD, metaName string) string {
	data := md.Get(metaName)
	if len(data) == 0 {
		return ""
	}

	return data[0]
}
