package transport

import (
	pb "github.com/runopsio/hoop/domain/proto"
	"github.com/runopsio/hoop/gateway/agent"
	"github.com/runopsio/hoop/gateway/connection"
	"github.com/runopsio/hoop/gateway/user"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"io"
	"log"
	"net"
	"strconv"
	"time"
)

type (
	Server struct {
		pb.UnimplementedTransportServer
		AgentService      agent.Service
		ConnectionService connection.Service
		UserService       user.Service
	}
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

func (s Server) Connect(stream pb.Transport_ConnectServer) error {
	log.Println("starting new grpc connection...")
	ctx := stream.Context()

	md, _ := metadata.FromIncomingContext(ctx)
	token := md.Get("authorization")[0]
	hostname := md.Get("hostname")[0]

	log.Printf("token: %s, hostname [%s]", token, hostname)

	agent, err := s.AgentService.FindOne(token)
	if err != nil || agent == nil {
		return status.Errorf(codes.Unauthenticated, "invalid token")
	}

	for {
		log.Println("start of iteration")
		select {
		case <-ctx.Done():
			log.Println("received DONE")
			return ctx.Err()
		default:
		}

		// receive data from stream
		req, err := stream.Recv()
		if err == io.EOF {
			// return will close stream from server side
			log.Println("EOF sent: exit")
			return nil
		}
		if err != nil {
			log.Printf("received error %v", err)
			continue
		}

		log.Printf("receive request type [%s] and component [%s]", req.Type, req.Component)

		// update max and send it to stream
		resp := pb.Packet{
			Component: "server",
			Type:      req.Type,
			Spec:      make(map[string][]byte),
			Payload:   []byte("payload as bytes"),
		}

		seconds, _ := strconv.Atoi(req.Type)
		if seconds == 8 {
			return ctx.Err()
		}

		go func(stream pb.Transport_ConnectServer) {
			time.Sleep(time.Millisecond * 1000 * time.Duration(seconds))
			log.Printf("sending response type [%s] and component [%s]", resp.Type, resp.Component)
			if err := stream.Send(&resp); err != nil {
				log.Printf("send error %v", err)
			}
		}(stream)
	}
}
