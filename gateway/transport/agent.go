package transport

import (
	"github.com/runopsio/hoop/gateway/plugin"
	"io"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

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

	config := plugin.Config{
		Org:           ag.OrgId,
		Hostname:      ag.Hostname,
		MachineId:     ag.MachineId,
		KernelVersion: ag.KernelVersion,
		ParamsData:    map[string]any{"client": pb.ConnectionOriginAgent},
	}

	bindAgent(ag.Id, stream)
	s.agentGracefulShutdown(ag)

	log.Printf("successful connection hostname: [%s], machineId [%s], kernelVersion [%s]", hostname, machineId, kernelVersion)
	agentErr := s.listenAgentMessages(config, ag, stream)
	if err := s.pluginOnDisconnect(config); err != nil {
		log.Printf("ua=agent - failed processing plugin on-disconnect phase, err=%v", err)
	}
	s.disconnectAgent(ag)
	return agentErr
}

func (s *Server) listenAgentMessages(config plugin.Config, ag *agent.Agent, stream pb.Transport_ConnectServer) error {
	ctx := stream.Context()

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		// receive data from stream
		pkt, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			if status, ok := status.FromError(err); ok && status.Code() == codes.Canceled {
				// TODO: send packet to agent to clean up resources
				log.Printf("id=%v - agent disconnected", ag.Id)
				return nil
			}
			log.Printf("received error from agent, err=%v", err)
			return err
		}
		if pb.PacketType(pkt.Type) == pb.PacketKeepAliveType {
			continue
		}
		sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
		if err := s.pluginOnReceive(config, pkt); err != nil {
			log.Printf("plugin reject packet, err=%v", err)
			return status.Errorf(codes.Internal, "internal error, plugin reject packet")
		}
		clientStream := getClientStream(sessionID)
		if clientStream == nil {
			// TODO: warn!
			log.Printf("client connection not found for session id [%s]", sessionID)
			continue
		}
		// log.Printf("received agent msg type [%s] and session id [%s]", pkt.Type, sessionID)
		s.processAgentPacket(pkt, clientStream)
	}
}

func (s *Server) processAgentPacket(pkt *pb.Packet, clientStream pb.Transport_ConnectServer) {
	_ = clientStream.Send(pkt)
}

func (s *Server) disconnectAgent(ag *agent.Agent) {
	unbindAgent(ag.Id)
	ag.Status = agent.StatusDisconnected
	s.AgentService.Persist(ag)
	log.Println("agent disconnected...")
}

func (s *Server) agentGracefulShutdown(ag *agent.Agent) {
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT,
		syscall.SIGKILL)
	go func() {
		<-sigc
		s.disconnectAgent(ag)
		os.Exit(143)
	}()
}
