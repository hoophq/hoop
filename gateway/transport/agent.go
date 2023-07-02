package transport

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/getsentry/sentry-go"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/monitoring"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	pbclient "github.com/runopsio/hoop/common/proto/client"
	pbgateway "github.com/runopsio/hoop/common/proto/gateway"
	"github.com/runopsio/hoop/gateway/agent"
	clientkeysstorage "github.com/runopsio/hoop/gateway/storagev2/clientkeys"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
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
	into.Hostname = mdget(md, "hostname")
	into.MachineId = mdget(md, "machine_id")
	into.KernelVersion = mdget(md, "kernel_version")
	into.Version = mdget(md, "version")
	into.GoVersion = mdget(md, "go-version")
	into.Compiler = mdget(md, "compiler")
	into.Platform = mdget(md, "platform")
}

func (s *Server) subscribeAgentSDK(stream pb.Transport_ConnectServer, dsn string) error {
	ctx := stream.Context()
	md, _ := metadata.FromIncomingContext(ctx)
	agentHostname := mdget(md, "hostname")
	clientKey, err := clientkeysstorage.ValidateDSN(s.StoreV2, dsn)
	if agentHostname == "" {
		return status.Errorf(codes.FailedPrecondition, "missing hostname header attribute")
	}
	agentHostname = strings.TrimSpace(strings.ToLower(agentHostname))
	switch {
	case err != nil:
		log.Errorf("failed validating dsn authentication, err=%v", err)
		return status.Errorf(codes.Internal, "failed validating authentication")
	case clientKey == nil:
		md.Delete("authorization")
		log.Debugf("invalid agent authentication, tokenlength=%v, client-metadata=%v", len(dsn), md)
		return status.Errorf(codes.Unauthenticated, "invalid authentication")
	}

	orgName, _ := s.UserService.GetOrgNameByID(clientKey.OrgID)
	clientOrigin := pb.ConnectionOriginAgent

	pluginContext := plugintypes.Context{
		OrgID:      clientKey.OrgID,
		ParamsData: map[string]any{"client": clientOrigin}}
	agentID := fmt.Sprintf("%s:%s", clientKey.Name, agentHostname)
	// TODO: in case of overwriting, send a disconnect to the old
	// stream
	bindAgent(agentID, stream)
	log.Infof("agent sdk connected: org=%v,name=%v,hostname=%v,platform=%v,version=%v",
		orgName, clientKey.Name, agentHostname, mdget(md, "platform"), mdget(md, "version"))

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
	pluginContext.ParamsData["disconnect-agent-id"] = agentID
	s.startDisconnectClientSink(agentID, clientOrigin, func(err error) {
		defer unbindAgent(agentID)
		_ = s.pluginOnDisconnect(pluginContext, err)
	})
	agentObj := &agent.Agent{Id: agentID, Name: clientKey.Name}
	agentErr = s.listenAgentMessages(&pluginContext, agentObj, stream)
	if agentErr == nil {
		log.Warnf("agent return a nil error, it will not disconnect it properly, id=%v", agentID)
	}
	s.disconnectClient(agentID, agentErr)
	return agentErr
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
	pluginContext := plugintypes.Context{
		OrgID:      ag.OrgId,
		ParamsData: map[string]any{"client": clientOrigin}}
	bindAgent(ag.Id, stream)

	log.Infof("agent connected: org=%v,name=%v,hostname=%v,platform=%v,version=%v,goversion=%v",
		orgName, ag.Name, ag.Hostname, ag.Platform, ag.Version, ag.GoVersion)

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
	pluginContext.ParamsData["disconnect-agent-id"] = ag.Id
	s.startDisconnectClientSink(ag.Id, clientOrigin, func(err error) {
		defer unbindAgent(ag.Id)
		ag.Status = agent.StatusDisconnected
		_, _ = s.AgentService.Persist(ag)
		_ = s.pluginOnDisconnect(pluginContext, err)
	})
	agentErr = s.listenAgentMessages(&pluginContext, ag, stream)
	s.disconnectClient(ag.Id, agentErr)
	return agentErr
}

func (s *Server) listenAgentMessages(pctx *plugintypes.Context, ag *agent.Agent, stream pb.Transport_ConnectServer) error {
	ctx := stream.Context()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// receive data from stream
		pkt, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				return io.EOF
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
		pctx.SID = sessionID
		// keep track of sessions being processed per agent
		agentSessionKeyID := fmt.Sprintf("%s:%s", ag.Id, sessionID)
		pctx.ParamsData[agentSessionKeyID] = nil
		log.With("session", sessionID).Debugf("receive agent packet type [%s]", pkt.Type)
		if _, err := s.pluginOnReceive(*pctx, pkt); err != nil {
			log.Warnf("plugin reject packet, err=%v", err)
			sentry.CaptureException(err)
			delete(pctx.ParamsData, agentSessionKeyID)
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
				delete(pctx.ParamsData, agentSessionKeyID)
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
