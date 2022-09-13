package transport

import (
	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/agent"
	"github.com/runopsio/hoop/gateway/client"
	"github.com/runopsio/hoop/gateway/connection"
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
		clients     map[string]pb.Transport_ConnectServer
		connections map[string]*connection.Connection
		mu          sync.Mutex
	}
)

var cc = connectedClients{
	clients:     make(map[string]pb.Transport_ConnectServer),
	connections: make(map[string]*connection.Connection),
	mu:          sync.Mutex{},
}

func (cc *connectedClients) bind(client *client.Client, stream pb.Transport_ConnectServer, connection *connection.Connection) {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	cc.clients[client.Id] = stream
	cc.connections[client.Id] = connection
}

func (cc *connectedClients) unbind(id string) {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	delete(cc.clients, id)
	delete(cc.connections, id)
}

func (cc *connectedClients) getClientStream(id string) pb.Transport_ConnectServer {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	return cc.clients[id]
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

	conn, err := s.ConnectionService.FindOne(context, connectionName)
	if err != nil {
		return status.Errorf(codes.Internal, err.Error())
	}
	if conn == nil {
		return status.Errorf(codes.NotFound, "connection not found")
	}

	ag, err := s.AgentService.FindById(conn.AgentId)
	if err != nil {
		return status.Errorf(codes.Internal, err.Error())
	}
	if ag == nil || ag.Status != agent.StatusConnected {
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
		ConnectionId:  conn.Id,
		AgentId:       conn.AgentId,
		Connection:    conn,
	}

	s.ClientService.Persist(c)
	cc.bind(c, stream, conn)

	log.Printf("successful connection hostname: [%s], machineId [%s], kernelVersion [%s]", hostname, machineId, kernelVersion)

	done := make(chan bool)
	go s.listenClientMessages(stream, c, done)
	<-done

	return nil
}

func (s *Server) listenClientMessages(stream pb.Transport_ConnectServer, c *client.Client, done chan bool) {
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
			done <- true
		default:
		}

		// receive data from stream
		req, err := stream.Recv()
		if err == io.EOF {
			log.Println("client disconnected...")
			c.Status = client.StatusDisconnected
			s.ClientService.Persist(c)
			cc.unbind(c.Id)
			done <- true
		}
		if err != nil {
			log.Printf("received error %v", err)
			continue
		}

		log.Printf("receive request type [%s] and component [%s]", req.Type, req.Component)

		// find original client and send response back

	}
}

func (s *Server) exchangeUserToken(token string) (string, error) {
	return "tester@hoop.dev", nil
}
