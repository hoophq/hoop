package transport

import (
	"fmt"
	"io"
	"log"
	"sync"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/client"
	"github.com/runopsio/hoop/gateway/connection"
	pb "github.com/runopsio/hoop/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
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

func bindClient(gwID string, stream pb.Transport_ConnectServer, connection *connection.Connection) {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	cc.clients[gwID] = stream
	cc.connections[gwID] = connection
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

	email, _ := s.exchangeUserToken(token)
	context, err := s.UserService.ContextByEmail(email)
	if err != nil || context == nil {
		return status.Errorf(codes.Unauthenticated, "invalid authentication")
	}

	conn, err := s.ConnectionService.FindOne(context, connectionName)
	if err != nil {
		return status.Errorf(codes.Internal, err.Error())
	}

	if conn == nil {
		return status.Errorf(codes.NotFound, fmt.Sprintf("connection '%v' not found", connectionName))
	}

	gatewayConnectionID := uuid.NewString()
	c := &client.Client{
		Id:            gatewayConnectionID,
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
	bindClient(gatewayConnectionID, stream, conn)

	log.Printf("successful connection hostname: [%s], machineId [%s], kernelVersion [%s]", hostname, machineId, kernelVersion)
	return s.listenClientMessages(stream, c, conn)
}

func (s *Server) listenClientMessages(stream pb.Transport_ConnectServer, c *client.Client, conn *connection.Connection) error {
	ctx := stream.Context()

	for {
		select {
		case <-ctx.Done():
			s.disconnectClient(c)
			return nil
		default:
		}

		// receive data from stream
		pkt, err := stream.Recv()
		if err != nil {
			defer s.disconnectClient(c)
			if err == io.EOF {
				return nil
			}
			log.Printf("received error from client: %v", err)
			return err
		}
		if pb.PacketType(pkt.Type) == pb.PacketKeepAliveType {
			continue
		}

		if pkt.Spec == nil {
			pkt.Spec = make(map[string][]byte)
		}
		pkt.Spec[pb.SpecGatewayConnectionID] = []byte(c.Id)

		// TODO: process router connect
		agStream := getAgentStream(conn.AgentId)
		if agStream == nil {
			log.Printf("agent connection not found for gateway connection id [%s]", c.Id)
			s.disconnectClient(c) // could we send a disconnect with an error?
			// TODO: send error back to client
			return nil
		}
		log.Printf("receive client packet type [%s] and gateway connection id [%s]",
			pkt.Type, c.Id)
		s.processClientPacket(pkt, c.Id, conn, agStream)
	}
}

func (s *Server) processClientPacket(
	pkt *pb.Packet,
	gwID string,
	conn *connection.Connection,
	agentStream pb.Transport_ConnectServer) {
	switch pb.PacketType(pkt.Type) {
	case pb.PacketGatewayConnectType:
		encEnvVars, err := pb.GobEncodeMap(conn.Secret)
		if err != nil {
			// TODO: send error back to client
			log.Printf("failed encoding secrets/env-var, err=%v", err)
			return
		}
		// TODO: deal with errors
		_ = agentStream.Send(&pb.Packet{
			Type: pb.PacketAgentConnectType.String(),
			Spec: map[string][]byte{
				pb.SpecGatewayConnectionID: []byte(gwID),
				pb.SpecAgentEnvVars:        encEnvVars,
			},
		})
	default:
		// default send to agent everything
		_ = agentStream.Send(pkt)
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
