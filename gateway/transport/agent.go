package transport

import (
	"github.com/getsentry/sentry-go"
	"io"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/runopsio/hoop/gateway/plugin"

	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	pbgateway "github.com/runopsio/hoop/common/proto/gateway"
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

func setAgentClientMetdata(into *agent.Agent, md metadata.MD) {
	into.Hostname = extractData(md, "hostname")
	into.MachineId = extractData(md, "machine_id")
	into.KernelVersion = extractData(md, "kernel_version")
	into.Version = extractData(md, "version")
	into.GoVersion = extractData(md, "go_version")
	into.Compiler = extractData(md, "compiler")
	into.Platform = extractData(md, "platform")
}

func (s *Server) subscribeAgent(stream pb.Transport_ConnectServer, token string) error {
	ctx := stream.Context()
	md, _ := metadata.FromIncomingContext(ctx)

	ag, err := s.AgentService.FindByToken(token)
	if err != nil || ag == nil {
		log.Printf("agent not found, err=%v", err)
		return status.Errorf(codes.Unauthenticated, "invalid authentication")
	}

	setAgentClientMetdata(ag, md)
	ag.Status = agent.StatusConnected
	_, err = s.AgentService.Persist(ag)
	if err != nil {
		log.Printf("failed saving agent connection, err=%v", err)
		sentry.CaptureException(err)
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

	log.Printf("agent connected: hostname=%v,platform=%v,version=%v,goversion=%v,compiler=%v,machineid=%v",
		ag.Hostname, ag.Platform, ag.Version, ag.GoVersion, ag.Compiler, ag.MachineId)
	_ = stream.Send(&pb.Packet{Type: pbagent.GatewayConnectOK})
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
			sentry.CaptureException(err)
			log.Printf("received error from agent, err=%v", err)
			return err
		}
		if pkt.Type == pbgateway.KeepAlive {
			continue
		}
		sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
		config.SessionId = sessionID
		// TODO: add debug logger
		// log.Printf("session=%s - receive agent packet type [%s]", sessionID, pkt.Type)
		if err := s.pluginOnReceive(config, pkt); err != nil {
			log.Printf("plugin reject packet, err=%v", err)
			sentry.CaptureException(err)
			return status.Errorf(codes.Internal, "internal error, plugin reject packet")
		}
		clientStream := getClientStream(sessionID)
		if clientStream == nil {
			log.Printf("session=%v - client connection not found, pkt=%v", sessionID, pkt.Type)
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
		syscall.SIGQUIT)
	go func() {
		<-sigc
		s.disconnectAgent(ag)
		os.Exit(143)
	}()
}
