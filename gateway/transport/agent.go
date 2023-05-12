package transport

import (
	"fmt"
	"io"
	"sync"

	"github.com/getsentry/sentry-go"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/monitoring"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	pbclient "github.com/runopsio/hoop/common/proto/client"
	pbgateway "github.com/runopsio/hoop/common/proto/gateway"
	"github.com/runopsio/hoop/gateway/agent"
	"github.com/runopsio/hoop/gateway/plugin"
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
	ca.mu.Lock()
	defer ca.mu.Unlock()
	return ca.agents[id]
}

func setAgentClientMetdata(into *agent.Agent, md metadata.MD) {
	into.Hostname = extractData(md, "hostname")
	into.MachineId = extractData(md, "machine_id")
	into.KernelVersion = extractData(md, "kernel_version")
	into.Version = extractData(md, "version")
	into.GoVersion = extractData(md, "go-version")
	into.Compiler = extractData(md, "compiler")
	into.Platform = extractData(md, "platform")
}

func (s *Server) subscribeAgent(stream pb.Transport_ConnectServer, token string) error {
	ctx := stream.Context()
	md, _ := metadata.FromIncomingContext(ctx)

	ag, err := s.AgentService.FindByToken(token)
	if err != nil || ag == nil {
		md.Delete("authorization")
		log.Debugf("invalid agent authentication, tokenlength=%v, client-metadata=%v", len(token), md)
		return status.Errorf(codes.Unauthenticated, "invalid authentication")
	}
	orgName, _ := s.UserService.GetOrgNameByID(ag.OrgId)

	setAgentClientMetdata(ag, md)
	ag.Status = agent.StatusConnected
	_, err = s.AgentService.Persist(ag)
	if err != nil {
		log.Errorf("failed saving agent connection, err=%v", err)
		sentry.CaptureException(err)
		return status.Errorf(codes.Internal, "internal error")
	}

	clientOrigin := pb.ConnectionOriginAgent
	config := plugin.Config{
		Org:           ag.OrgId,
		Hostname:      ag.Hostname,
		MachineId:     ag.MachineId,
		KernelVersion: ag.KernelVersion,
		ParamsData:    map[string]any{"client": clientOrigin},
	}

	bindAgent(ag.Id, stream)

	log.Infof("agent connected: org=%v,name=%v,hostname=%v,platform=%v,version=%v,goversion=%v,compiler=%v",
		orgName, ag.Name, ag.Hostname, ag.Platform, ag.Version, ag.GoVersion, ag.Compiler)

	var transportConfigBytes []byte
	if s.PyroscopeIngestURL != "" {
		transportConfigBytes, _ = pb.GobEncode(monitoring.TransportConfig{
			Sentry: monitoring.SentryConfig{
				DSN:         s.AgentSentryDSN,
				OrgName:     orgName,
				Environment: s.IDProvider.ApiURL,
			},
			Profiler: monitoring.ProfilerConfig{
				PyroscopeServerAddress: s.PyroscopeIngestURL,
				PyroscopeAuthToken:     s.PyroscopeAuthToken,
				OrgName:                orgName,
				Environment:            s.IDProvider.ApiURL,
			},
		})
	}
	_ = stream.Send(&pb.Packet{
		Type:    pbagent.GatewayConnectOK,
		Payload: transportConfigBytes,
	})
	var agentErr error
	config.ParamsData["disconnect-agent-id"] = ag.Id
	s.startDisconnectClientSink(ag.Id, clientOrigin, func(err error) {
		defer unbindAgent(ag.Id)
		ag.Status = agent.StatusDisconnected
		_, _ = s.AgentService.Persist(ag)
		_ = s.pluginOnDisconnect(config, err)
	})
	agentErr = s.listenAgentMessages(&config, ag, stream)
	s.disconnectClient(ag.Id, agentErr)
	return agentErr
}

func (s *Server) listenAgentMessages(config *plugin.Config, ag *agent.Agent, stream pb.Transport_ConnectServer) error {
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
				log.Warnf("id=%v, name=%v - agent disconnected", ag.Id, ag.Name)
				return fmt.Errorf("agent %v disconnected, reason=%v", ag.Name, err)
			}
			sentry.CaptureException(err)
			log.Errorf("received error from agent %v, err=%v", ag.Name, err)
			return err
		}
		if pkt.Type == pbgateway.KeepAlive || pkt.Type == "KeepAlive" {
			continue
		}
		sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
		config.SessionId = sessionID
		// keep track of sessions being processed per agent
		agentSessionKeyID := fmt.Sprintf("%s:%s", ag.Id, sessionID)
		config.ParamsData[agentSessionKeyID] = nil
		log.With("session", sessionID).Debugf("receive agent packet type [%s]", pkt.Type)
		if err := s.pluginOnReceive(*config, pkt, func(err error) error { return err }); err != nil {
			// TODO: add plugin name
			log.Warnf("plugin reject packet, err=%v", err)
			sentry.CaptureException(err)
			delete(config.ParamsData, agentSessionKeyID)
			return status.Errorf(codes.Internal, "internal error, plugin reject packet")
		}

		if pb.PacketType(pkt.Type) == pbclient.SessionClose {
			if sessionID := pkt.Spec[pb.SpecGatewaySessionID]; len(sessionID) > 0 {
				var trackErr error
				if len(pkt.Payload) > 0 {
					trackErr = fmt.Errorf(string(pkt.Payload))
				}
				s.trackSessionStatus(string(sessionID), pb.SessionPhaseClientSessionClose, trackErr)
				s.disconnectClient(string(sessionID), trackErr)
				// now it's safe to remove the session from memory
				delete(config.ParamsData, agentSessionKeyID)
			}
		}
		if clientStream := getClientStream(sessionID); clientStream != nil {
			s.processAgentPacket(pkt, clientStream)
		}
	}
}

func (s *Server) processAgentPacket(pkt *pb.Packet, clientStream pb.Transport_ConnectServer) {
	_ = clientStream.Send(pkt)
}
