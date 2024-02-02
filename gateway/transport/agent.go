package transport

import (
	"context"
	"fmt"
	"io"
	"sort"
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
	apitypes "github.com/runopsio/hoop/gateway/apiclient/types"
	pgusers "github.com/runopsio/hoop/gateway/pgrest/users"
	authinterceptor "github.com/runopsio/hoop/gateway/transport/interceptors/auth"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
	"github.com/runopsio/hoop/gateway/user"
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

	if strings.Contains(agentId, ",") {
		for _, id := range strings.Split(agentId, ",") {
			ca.agents[id] = stream
		}
		return
	}
	ca.agents[agentId] = stream
}

func unbindAgent(agentId string) {
	ca.mu.Lock()
	defer ca.mu.Unlock()

	if strings.Contains(agentId, ",") {
		for _, id := range strings.Split(agentId, ",") {
			delete(ca.agents, id)
		}
		return
	}
	delete(ca.agents, agentId)
}

// hasAgentStream validates if there's an agent connected
func hasAgentStream(agentID string) bool { return getAgentStream(agentID) != nil }
func getAgentStream(id string) pb.Transport_ConnectServer {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	return ca.agents[id]
}

func normalizeAgentID(orgID, resourceName string, connectionItems []string) string {
	var items []string
	if len(connectionItems) == 0 {
		return ""
	}
	for _, connName := range connectionItems {
		connName = strings.TrimSpace(strings.ToLower(connName))
		connName = fmt.Sprintf("%s:%s:%s", orgID, resourceName, connName)
		items = append(items, connName)
	}
	sort.Strings(items)
	return strings.Join(items, ",")
}

func (s *Server) subscribeAgent(grpcStream pb.Transport_ConnectServer) error {
	ctx := grpcStream.Context()
	md, _ := metadata.FromIncomingContext(ctx)

	var gwctx authinterceptor.GatewayContext
	ctxVal, err := authinterceptor.GetGatewayContext(ctx)
	if err != nil {
		log.Error(err)
		return err
	}
	connectionItems := mdget(md, "connection-items")
	var connectionNameList []string
	if connectionItems != "" {
		connectionNameList = strings.Split(connectionItems, ",")
	}
	var agentBindID string
	switch v := ctxVal.(type) {
	case *authinterceptor.GatewayContext:
		gwctx = *v
		agentBindID = gwctx.Agent.ID
		if gwctx.Agent.Mode == pb.AgentModeEmbeddedType && len(connectionNameList) > 0 {
			agentBindID = normalizeAgentID(gwctx.Agent.OrgID, gwctx.Agent.Name, connectionNameList)
		}
	default:
		log.Warnf("failed authenticating, could not assign authentication context, type=%T", ctxVal)
		return status.Error(codes.Unauthenticated, "invalid authentication, could not assign authentication context")
	}
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
	bindAgent(agentBindID, stream)

	log.With("bind-id", agentBindID).Infof("agent connected: %s", gwctx.Agent)
	_ = stream.Send(&pb.Packet{
		Type:    pbagent.GatewayConnectOK,
		Payload: s.configurationData(orgName),
	})
	var agentErr error
	pluginContext.ParamsData["disconnect-agent-id"] = gwctx.Agent.ID
	s.startDisconnectClientSink(agentBindID, clientOrigin, func(err error) {
		defer unbindAgent(agentBindID)
		if err := s.updateAgentStatus(agent.StatusDisconnected, gwctx.Agent); err != nil {
			log.Warnf("failed publishing disconnect agent state, org=%v, name=%v, err=%v", gwctx.Agent, err)
		}
		// TODO: need to disconnect all proxy clients connected
		// or implement a reconnect strategy in the proxy client
		stream.Disconnect(context.Canceled)
		_ = s.pluginOnDisconnect(pluginContext, err)
	})
	agentErr = s.listenAgentMessages(&pluginContext, &gwctx.Agent, stream)
	DisconnectClient(agentBindID, agentErr)
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
	// client keys doesn't have an agent record, it should be ignored
	if strings.HasPrefix(agentCtx.Name, "clientkey:") {
		return nil
	}
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
