package transport

import (
	"github.com/runopsio/hoop/gateway/agent"
	pb "github.com/runopsio/hoop/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"log"
	"sync"
)

type (
	connectedAgents struct {
		agents map[string]pb.Transport_ConnectServer
		mu     sync.Mutex
	}
)

var ca = connectedAgents{
	agents: make(map[string]pb.Transport_ConnectServer),
	mu:     sync.Mutex{},
}

func bindAgent(agentId string, stream pb.Transport_ConnectServer) {
	ca.mu.Lock()
	defer ca.mu.Unlock()

	ca.agents[agentId] = stream
}

func unbindAgent(agentId string) {
	ca.mu.Lock()
	defer ca.mu.Unlock()

	delete(ca.agents, agentId)
}

func getAgentStream(id string) pb.Transport_ConnectServer {
	return ca.agents[id]
}

func (s *Server) subscribeAgent(stream pb.Transport_ConnectServer, token string) error {
	ctx := stream.Context()
	md, _ := metadata.FromIncomingContext(ctx)

	hostname := extractData(md, "hostname")
	machineId := extractData(md, "machine_id")
	kernelVersion := extractData(md, "kernel_version")

	ag, err := s.AgentService.FindByToken(token)
	if err != nil || ag == nil {
		return status.Errorf(codes.Unauthenticated, "invalid authentication")
	}

	ag.Hostname = hostname
	ag.MachineId = machineId
	ag.KernelVersion = kernelVersion
	ag.Status = agent.StatusConnected

	_, err = s.AgentService.Persist(ag)
	if err != nil {
		return status.Errorf(codes.Internal, "internal error")
	}

	bindAgent(ag.Id, stream)

	log.Printf("successful connection hostname: [%s], machineId [%s], kernelVersion [%s]", hostname, machineId, kernelVersion)

	go s.startKeepAlive(stream)
	s.listenAgentMessages(ag, stream)

	return nil
}

func (s *Server) listenAgentMessages(ag *agent.Agent, stream pb.Transport_ConnectServer) {
	ctx := stream.Context()

	for {
		select {
		case <-ctx.Done():
			s.disconnectAgent(ag)
			return
		default:
		}

		// receive data from stream
		reqPacket, err := stream.Recv()
		if err != nil {
			log.Printf("received error from agent: %v", err)
			s.disconnectAgent(ag)
			return
		}

		go s.processAgentMsg(reqPacket)
	}
}

func (s *Server) processAgentMsg(packet *pb.Packet) {
	log.Printf("receive request type [%s] and component [%s]", packet.Type, packet.Component)

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

func (s *Server) disconnectAgent(ag *agent.Agent) {
	unbindAgent(ag.Token)
	ag.Status = agent.StatusDisconnected
	s.AgentService.Persist(ag)
	log.Println("agent disconnected...")
}
