package transport

import (
	"github.com/google/uuid"
	agent2 "github.com/runopsio/hoop/gateway/agent"
	client "github.com/runopsio/hoop/gateway/client"
	pb "github.com/runopsio/hoop/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"io"
	"log"
	"sync"
	"time"
)

type (
	connectedClients struct {
		clients map[string]pb.Transport_ConnectServer
		mu      sync.Mutex
	}
)

var cc = connectedClients{
	clients: make(map[string]pb.Transport_ConnectServer),
	mu:      sync.Mutex{},
}

func (cc *connectedClients) bind(client *client.Client, stream pb.Transport_ConnectServer) {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	cc.clients[client.Id] = stream
}

func (cc *connectedClients) unbind(id string) {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	delete(cc.clients, id)
}

func (s *Server) subscribeClient(stream pb.Transport_ConnectServer, token string) error {
	ctx := stream.Context()
	md, _ := metadata.FromIncomingContext(ctx)

	hostname := extractData(md, "hostname")
	machineId := extractData(md, "machine_id")
	kernelVersion := extractData(md, "kernel_version")
	connectionName := extractData(md, "connection_name")

	email, err := s.exchangeUserToken(token)

	context, err := s.UserService.ContextByEmail(email)
	if err != nil || context == nil {
		return status.Errorf(codes.Unauthenticated, "invalid authentication")
	}

	connection, err := s.ConnectionService.FindOne(context, connectionName)
	if err != nil {
		return status.Errorf(codes.Internal, err.Error())
	}
	if connection == nil {
		return status.Errorf(codes.NotFound, "connection not found")
	}

	agent, err := s.AgentService.FindById(connection.AgentId)
	if err != nil {
		return status.Errorf(codes.Internal, err.Error())
	}
	if agent == nil || agent.Status == agent2.StatusDisconnected {
		return status.Errorf(codes.FailedPrecondition, "agent is offline")
	}

	c := &client.Client{
		Id:            uuid.NewString(),
		OrgId:         context.Org.Id,
		UserId:        context.User.Id,
		Hostname:      hostname,
		MachineId:     machineId,
		KernelVersion: kernelVersion,
		Status:        client.StatusConnected,
		ConnectionId:  connection.Id,
		AgentId:       connection.AgentId,
	}

	s.ClientService.Persist(c)
	cc.bind(c, stream)

	log.Printf("successful connection hostname: [%s], machineId [%s], kernelVersion [%s]", hostname, machineId, kernelVersion)
	s.listenClientMessages(stream, c)

	return nil
}

func (s *Server) listenClientMessages(stream pb.Transport_ConnectServer, c *client.Client) {
	ctx := stream.Context()

	// keep alive msg
	go func() {
		for {
			proto := &pb.Packet{
				Component: pb.PacketGatewayComponent,
				Type:      pb.PacketKeepAliveType,
			}
			log.Println("sending keep alive command")
			if err := stream.Send(proto); err != nil {
				if err != nil {
					log.Printf("failed sending keep alive command, err=%v", err)
					break
				}
			}
			time.Sleep(time.Second * 10)
		}
	}()

	for {
		log.Println("start of iteration")
		select {
		case <-ctx.Done():
			log.Println("client disconnected...")
			c.Status = client.StatusDisconnected
			s.ClientService.Persist(c)
			cc.unbind(c.Id)
			return
		default:
		}

		// receive data from stream
		req, err := stream.Recv()
		if err == io.EOF {
			log.Println("client disconnected...")
			c.Status = client.StatusDisconnected
			s.ClientService.Persist(c)
			cc.unbind(c.Id)
			return
		}
		if err != nil {
			log.Printf("received error %v", err)
			continue
		}

		log.Printf("receive request type [%s] and component [%s]", req.Type, req.Component)

		// find original client and send response back

		//resp := pb.Packet{
		//	Component: "server",
		//	Type:      req.Type,
		//	Spec:      make(map[string][]byte),
		//	Payload:   []byte("payload as bytes"),
		//}
		//
		//go func(stream pb.Transport_ConnectServer) {
		//	log.Printf("sending response type [%s] and component [%s]", resp.Type, resp.Component)
		//	if err := stream.Send(&resp); err != nil {
		//		log.Printf("send error %v", err)
		//	}
		//}(stream)
	}
}

func (s *Server) exchangeUserToken(token string) (string, error) {
	return "tester@hoop.dev", nil
}
