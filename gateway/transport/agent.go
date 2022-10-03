package transport

import (
	"io"
	"log"
	"sync"

	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/agent"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
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
	return s.listenAgentMessages(ag, stream)
}

func (s *Server) listenAgentMessages(ag *agent.Agent, stream pb.Transport_ConnectServer) error {
	ctx := stream.Context()

	for {
		select {
		case <-ctx.Done():
			s.disconnectAgent(ag)
			return nil
		default:
		}

		// receive data from stream
		pkt, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				s.disconnectAgent(ag)
				return nil
			}
			if status, ok := status.FromError(err); ok && status.Code() == codes.Canceled {
				// TODO: send packet to agent to clean up resources
				log.Printf("id=%v - agent disconnected", ag.Id)
				return nil
			}
			log.Printf("received error from agent, err=%v", err)
			s.disconnectAgent(ag)
			return err
		}
		if pb.PacketType(pkt.Type) == pb.PacketKeepAliveType {
			continue
		}
		gwID := string(pkt.Spec[pb.SpecGatewayConnectionID])
		clientStream := getClientStream(gwID)
		if clientStream == nil {
			// TODO: warn!
			log.Printf("client connection not found for gateway connection id [%s]", gwID)
			continue
		}
		log.Printf("received agent msg type [%s] and gateway connection id [%s]", pkt.Type, gwID)
		s.processAgentPacket(pkt, clientStream)
	}
}

func (s *Server) processAgentPacket(pkt *pb.Packet, clientStream pb.Transport_ConnectServer) {
	_ = clientStream.Send(pkt)
}

func (s *Server) disconnectAgent(ag *agent.Agent) {
	unbindAgent(ag.Token)
	ag.Status = agent.StatusDisconnected
	s.AgentService.Persist(ag)
	log.Println("agent disconnected...")
}
