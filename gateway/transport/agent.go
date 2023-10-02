package transport

import (
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
	apitypes "github.com/runopsio/hoop/gateway/apiclient/types"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
	authinterceptor "github.com/runopsio/hoop/gateway/transportv2/interceptors/auth"
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

// Deprecated: subscribeAgentSidecar is deprecated in flavor of subscribeAgent
func (s *Server) subscribeAgentSidecar(stream pb.Transport_ConnectServer) error {
	ctx := stream.Context()
	md, _ := metadata.FromIncomingContext(ctx)

	var clientKey types.ClientKey
	err := authinterceptor.ParseGatewayContextInto(ctx, &clientKey)
	if err != nil {
		log.Error(err)
		return err
	}

	var agentID string
	// connection-name header is keep for compatibility with old agents
	connectionName := mdget(md, "connection-name")
	connectionItems := mdget(md, "connection-items")
	switch {
	case connectionName != "":
		agentID = normalizeAgentID(clientKey.OrgID, clientKey.Name, []string{connectionName})
		log.Warnf("agent %v using deprecated header CONNECTION-NAME", mdget(md, "version"))
	case connectionItems != "":
		agentID = normalizeAgentID(clientKey.OrgID, clientKey.Name, strings.Split(connectionItems, ","))
	}
	if agentID == "" {
		log.Error("missing required connection-items attribute, connection-name=%v, connection-items=%v, err=%v",
			connectionName, connectionItems, err)
		sentry.CaptureException(err)
		return status.Errorf(codes.Internal, "missing connection-items header")
	}

	orgName, _ := s.UserService.GetOrgNameByID(clientKey.OrgID)
	clientOrigin := pb.ConnectionOriginAgent

	pluginContext := plugintypes.Context{
		OrgID:        clientKey.OrgID,
		ClientOrigin: clientOrigin,
		ParamsData:   map[string]any{"client": clientOrigin},
	}
	// TODO: in case of overwriting, send a disconnect to the old
	// stream
	bindAgent(agentID, stream)
	log.Infof("agent sidecar connected: org=%v,key=%v,id=%v,platform=%v,version=%v",
		orgName, clientKey.Name, agentID, mdget(md, "platform"), mdget(md, "version"))

	_ = stream.Send(&pb.Packet{
		Type:    pbagent.GatewayConnectOK,
		Payload: s.configurationData(orgName),
	})
	var agentErr error
	pluginContext.ParamsData["disconnect-agent-id"] = agentID
	s.startDisconnectClientSink(agentID, clientOrigin, func(err error) {
		defer unbindAgent(agentID)
		_ = s.pluginOnDisconnect(pluginContext, err)
	})
	agentObj := &apitypes.Agent{ID: agentID, Name: clientKey.Name}
	agentErr = s.listenAgentMessages(&pluginContext, agentObj, stream)
	if agentErr == nil {
		log.Warnf("agent return a nil error, it will not disconnect it properly, id=%v", agentID)
	}
	s.disconnectClient(agentID, agentErr)
	return agentErr
}

func (s *Server) subscribeAgent(stream pb.Transport_ConnectServer) error {
	ctx := stream.Context()
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
	// TODO: it should be removed after there're no more client keys being used
	case *types.ClientKey:
		agentBindID = normalizeAgentID(v.OrgID, v.Name, connectionNameList)
		if agentBindID == "" {
			log.Error("missing required connection-items attribute, connection-items=%v, err=%v",
				connectionItems, err)
			sentry.CaptureException(err)
			return status.Errorf(codes.Internal, "missing connection-items header")
		}

		gwctx.Agent.ID = agentBindID
		gwctx.Agent.Mode = pb.AgentModeEmbeddedType
		gwctx.Agent.Name = fmt.Sprintf("clientkey:%s", v.Name)
		gwctx.Agent.OrgID = v.OrgID
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
	orgName, _ := s.UserService.GetOrgNameByID(gwctx.Agent.OrgID)

	clientOrigin := pb.ConnectionOriginAgent
	pluginContext := plugintypes.Context{
		OrgID:        gwctx.Agent.OrgID,
		ClientOrigin: clientOrigin,
		ParamsData:   map[string]any{"client": clientOrigin},
	}
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
		if err := publishAgentDisconnect(s.IDProvider.ApiURL, gwctx.BearerToken); err != nil {
			log.Warnf("failed publishing disconnect agent state, err=%v", err)
		}
		_ = s.pluginOnDisconnect(pluginContext, err)
	})
	agentErr = s.listenAgentMessages(&pluginContext, &gwctx.Agent, stream)
	s.disconnectClient(agentBindID, agentErr)
	return agentErr
}

func (s *Server) listenAgentMessages(pctx *plugintypes.Context, ag *apitypes.Agent, stream pb.Transport_ConnectServer) error {
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
