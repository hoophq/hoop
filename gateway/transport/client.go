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
	"log"
	"sync"
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

func bindClient(client *client.Client, stream pb.Transport_ConnectServer, connection *connection.Connection) {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	cc.clients[client.Id] = stream
	cc.connections[client.Id] = connection
}

func unbindClient(id string) {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	delete(cc.clients, id)
	delete(cc.connections, id)
}

func getClientStream(id string) pb.Transport_ConnectServer {
	return cc.clients[id]
}

func getClientConnection(id string) *connection.Connection {
	return cc.connections[id]
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
	}

	s.ClientService.Persist(c)
	bindClient(c, stream, conn)

	log.Printf("successful connection hostname: [%s], machineId [%s], kernelVersion [%s]", hostname, machineId, kernelVersion)

	go s.startKeepAlive(stream)
	s.listenClientMessages(stream, c)

	return nil
}

func (s *Server) listenClientMessages(stream pb.Transport_ConnectServer, c *client.Client) {
	ctx := stream.Context()

	for {
		select {
		case <-ctx.Done():
			s.disconnectClient(c)
			return
		default:
		}

		// receive data from stream
		reqPacket, err := stream.Recv()
		if err != nil {
			log.Printf("received error from client: %v", err)
			s.disconnectClient(c)
			return
		}

		if reqPacket.Spec == nil {
			reqPacket.Spec = make(map[string][]byte)
		}
		reqPacket.Spec["client_id"] = []byte(c.Id)

		conn := getClientConnection(c.Id)
		if conn == nil {
			log.Printf("connection not found for client_id [%s]", c.Id)
			s.disconnectClient(c)
			return
		}

		agStream := getAgentStream(conn.AgentId)
		if agStream == nil {
			log.Printf("agent not found for client_id [%s]", c.Id)
			s.disconnectClient(c)
			return
		}

		go s.processClientMsg(reqPacket, agStream)
	}
}

func (s *Server) processClientMsg(packet *pb.Packet, agStream pb.Transport_ConnectServer) {
	clientId := string(packet.Spec["client_id"])
	log.Printf("receive client msg type [%s] and component [%s] and client_id [%s]", packet.Type, packet.Component, clientId)

	switch t := packet.Type; t {

	case pb.PacketKeepAliveType:
		return

	case pb.PacketDataStreamType:
		if err := agStream.Send(packet); err != nil {
			log.Printf("send error %v", err)
		}
	}
}

func (s *Server) disconnectClient(c *client.Client) {
	unbindClient(c.Id)
	c.Status = client.StatusDisconnected
	s.ClientService.Persist(c)
	log.Println("client disconnected...")
}

func (s *Server) exchangeUserToken(token string) (string, error) {
	return "tester@hoop.dev", nil
}
