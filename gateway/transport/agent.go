package transport

import (
	"context"
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
	apitypes "github.com/runopsio/hoop/gateway/apiclient/types"
	pgusers "github.com/runopsio/hoop/gateway/pgrest/users"
	"github.com/runopsio/hoop/gateway/transport/connectionrequests"
	authinterceptor "github.com/runopsio/hoop/gateway/transport/interceptors/auth"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
	"github.com/runopsio/hoop/gateway/user"
	"google.golang.org/grpc/codes"
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

// hasAgentStream validates if there's an agent connected
func hasAgentStream(agentID string) bool { return getAgentStream(agentID) != nil }
func getAgentStream(id string) pb.Transport_ConnectServer {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	return ca.agents[id]
}

func (s *Server) subscribeAgent(grpcStream pb.Transport_ConnectServer) error {
	var gwctx authinterceptor.GatewayContext
	err := authinterceptor.ParseGatewayContextInto(grpcStream.Context(), &gwctx)
	if err != nil {
		log.Error(err)
		return err
	}
	agentID := gwctx.Agent.ID
	if hasAgentStream(agentID) {
		log.Warnf("agent %s is already connected", gwctx.Agent.Name)
		return status.Errorf(codes.FailedPrecondition, "agent %s already connected", gwctx.Agent.Name)
	}
	// TODO: refactor me, obtain the org name in the authentication layer interceptor
	org, _ := pgusers.New().FetchOrgByID(gwctx.Agent.OrgID)
	var orgName string
	if org != nil {
		orgName = org.Name
	}
	clientOrigin := pb.ConnectionOriginAgent
	pluginContext := plugintypes.Context{
		OrgID:        gwctx.Agent.OrgID,
		ClientOrigin: clientOrigin,
		ParamsData:   map[string]any{"client": clientOrigin},
	}
	if err := s.updateAgentStatus(agent.StatusConnected, gwctx.Agent); err != nil {
		log.Errorf("failed updating agent to connected status, err=%v", err)
		sentry.CaptureException(err)
		return status.Errorf(codes.Internal, "failed updating agent, internal error")
	}
	stream := newStreamWrapper(grpcStream, gwctx.Agent.OrgID)
	bindAgent(agentID, stream)

	connectionrequests.AcceptProxyConnection(gwctx.Agent.OrgID, agentID, nil)
	log.With("agentid", agentID).Infof("agent connected: %s", gwctx.Agent)
	_ = stream.Send(&pb.Packet{
		Type:    pbagent.GatewayConnectOK,
		Payload: s.configurationData(orgName),
	})
	var agentErr error
	pluginContext.ParamsData["disconnect-agent-id"] = gwctx.Agent.ID
	s.startDisconnectClientSink(agentID, clientOrigin, func(err error) {
		defer unbindAgent(agentID)
		if err := s.updateAgentStatus(agent.StatusDisconnected, gwctx.Agent); err != nil {
			log.Warnf("failed publishing disconnect agent state, org=%v, name=%v, err=%v", gwctx.Agent, err)
		}
		// TODO: need to disconnect all proxy clients connected
		// or implement a reconnect strategy in the proxy client
		stream.Disconnect(context.Canceled)
		_ = s.pluginOnDisconnect(pluginContext, err)
	})
	agentErr = s.listenAgentMessages(&pluginContext, &gwctx.Agent, stream)
	DisconnectClient(agentID, agentErr)
	return agentErr
}

func (s *Server) listenAgentMessages(pctx *plugintypes.Context, ag *apitypes.Agent, stream streamWrapper) error {
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
				return fmt.Errorf("agent %v disconnected, end-of-file stream", ag.Name)
			}
			if status, ok := status.FromError(err); ok && status.Code() == codes.Canceled {
				// TODO: send packet to agent to clean up resources
				log.Warnf("id=%v, name=%v - agent disconnected", ag.ID, ag.Name)
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
		agentSessionKeyID := fmt.Sprintf("%s:%s", ag.ID, sessionID)
		pctx.ParamsData[agentSessionKeyID] = nil
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
				DisconnectClient(string(sessionID), trackErr)
				// now it's safe to remove the session from memory
				delete(pctx.ParamsData, agentSessionKeyID)
			}
		}
		if clientStream := getClientStream(sessionID); clientStream != nil {
			_ = clientStream.Send(pkt)
		}
	}
}

func (s *Server) updateAgentStatus(agentStatus agent.Status, agentCtx apitypes.Agent) error {
	ag, err := s.AgentService.FindByNameOrID(user.NewContext(agentCtx.OrgID, ""), agentCtx.Name)
	if err != nil || ag == nil {
		return fmt.Errorf("failed to obtain agent org=%v, name=%v, err=%v", agentCtx.OrgID, agentCtx.Name, err)
	}
	if agentStatus == agent.StatusConnected {
		ag.Hostname = agentCtx.Metadata.Hostname
		ag.MachineId = agentCtx.Metadata.MachineID
		ag.KernelVersion = agentCtx.Metadata.KernelVersion
		ag.Version = agentCtx.Metadata.Version
		ag.GoVersion = agentCtx.Metadata.GoVersion
		ag.Compiler = agentCtx.Metadata.Compiler
		ag.Platform = agentCtx.Metadata.Platform
	}
	// set platform to empty string when agent is disconnected
	// it will allow to identify embedded agents connected status
	if agentStatus == agent.StatusDisconnected {
		ag.Platform = ""
	}
	ag.Status = agentStatus
	_, err = s.AgentService.Persist(ag)
	return err
}

func (s *Server) configurationData(orgName string) []byte {
	var transportConfigBytes []byte
	if s.PyroscopeIngestURL != "" {

		transportConfigBytes, _ = pb.GobEncode(monitoring.TransportConfig{
			Sentry: monitoring.SentryConfig{
				DSN:         s.AgentSentryDSN,
				OrgName:     orgName,
				Environment: monitoring.NormalizeEnvironment(s.IDProvider.ApiURL),
			},
			Profiler: monitoring.ProfilerConfig{
				PyroscopeServerAddress: s.PyroscopeIngestURL,
				PyroscopeAuthToken:     s.PyroscopeAuthToken,
				OrgName:                orgName,
				Environment:            monitoring.NormalizeEnvironment(s.IDProvider.ApiURL),
			},
		})
	}
	return transportConfigBytes
}

func DisconnectAllAgentsByOrg(orgID string, err error) int {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	count := 0
	for agentID, obj := range ca.agents {
		if stream, ok := obj.(streamWrapper); ok {
			if stream.orgID == orgID {
				count++
				DisconnectClient(agentID, err)
			}
		}
	}
	return count
}
