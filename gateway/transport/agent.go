package transport

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"github.com/getsentry/sentry-go"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/monitoring"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	pbclient "github.com/runopsio/hoop/common/proto/client"
	pbgateway "github.com/runopsio/hoop/common/proto/gateway"
	"github.com/runopsio/hoop/gateway/agent"
	"github.com/runopsio/hoop/gateway/storagev2/types"
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

func setAgentClientMetdata(into *agent.Agent, md metadata.MD) {
	into.Hostname = mdget(md, "hostname")
	into.MachineId = mdget(md, "machine_id")
	into.KernelVersion = mdget(md, "kernel_version")
	into.Version = mdget(md, "version")
	into.GoVersion = mdget(md, "go-version")
	into.Compiler = mdget(md, "compiler")
	into.Platform = mdget(md, "platform")
}

func normalizeAgentID(prefix string, connectionItems []string) string {
	var items []string
	if len(connectionItems) == 0 {
		return ""
	}
	for _, connName := range connectionItems {
		connName = strings.TrimSpace(strings.ToLower(connName))
		connName = fmt.Sprintf("%s:%s", prefix, connName)
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
	err := parseGatewayContextInto(ctx, &clientKey)
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
		agentID = normalizeAgentID(clientKey.Name, []string{connectionName})
		log.Warnf("agent %v using deprecated header CONNECTION-NAME", mdget(md, "version"))
	case connectionItems != "":
		agentID = normalizeAgentID(clientKey.Name, strings.Split(connectionItems, ","))
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
		OrgID:      clientKey.OrgID,
		ParamsData: map[string]any{"client": clientOrigin}}
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
	agentObj := &agent.Agent{Id: agentID, Name: clientKey.Name}
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

	var ag agent.Agent
	ctxVal, err := getGatewayContext(ctx)
	if err != nil {
		log.Error(err)
		return err
	}
	agentMode := pb.AgentModeDefaultType
	switch v := ctxVal.(type) {
	case *types.ClientKey:
		agentMode = v.AgentMode
		// use a prefix to avoid colission with old agent keys
		agentName := fmt.Sprintf("clientkey:%s", v.Name)
		// generate a deterministic uuid based on the client key name
		ag.Id = uuid.NewSHA1(uuid.NameSpaceURL, []byte(agentName)).String()
		ag.OrgId = v.OrgID
		ag.Name = agentName
		// it's safe to keep token empty until we remove agent keys authentication.
		// the authentication mechanism will not validate empty tokens in the store.
		ag.Token = ""
		if agentMode == pb.AgentModeEmbeddedType {
			connectionItems := mdget(md, "connection-items")
			ag.Id = normalizeAgentID(v.Name, strings.Split(connectionItems, ","))
			if ag.Id == "" {
				log.Error("missing required connection-items attribute, connection-items=%v, err=%v",
					connectionItems, err)
				sentry.CaptureException(err)
				return status.Errorf(codes.Internal, "missing connection-items header")
			}
		}
	case *agent.Agent:
		ag = *v
	default:
		log.Warnf("failed authenticating, could not assign authentication context, type=%T", ctxVal)
		return status.Error(codes.Unauthenticated, "invalid authentication, could not assign authentication context")
	}
	orgName, _ := s.UserService.GetOrgNameByID(ag.OrgId)

	setAgentClientMetdata(&ag, md)
	ag.Status = agent.StatusConnected
	if err := s.updateAgentStatus(agentMode, &ag); err != nil {
		log.Errorf("failed saving agent connection, err=%v", err)
		sentry.CaptureException(err)
		return status.Errorf(codes.Internal, "internal error")
	}

	clientOrigin := pb.ConnectionOriginAgent
	pluginContext := plugintypes.Context{
		OrgID:      ag.OrgId,
		ParamsData: map[string]any{"client": clientOrigin}}
	bindAgent(ag.Id, stream)

	log.Infof("agent connected: org=%v,name=%v,mode=%v,hostname=%v,platform=%v,version=%v,goversion=%v",
		orgName, ag.Name, agentMode, ag.Hostname, ag.Platform, ag.Version, ag.GoVersion)

	_ = stream.Send(&pb.Packet{
		Type:    pbagent.GatewayConnectOK,
		Payload: s.configurationData(orgName),
	})
	var agentErr error
	pluginContext.ParamsData["disconnect-agent-id"] = ag.Id
	s.startDisconnectClientSink(ag.Id, clientOrigin, func(err error) {
		defer unbindAgent(ag.Id)
		ag.Status = agent.StatusDisconnected
		_ = s.updateAgentStatus(agentMode, &ag)
		_ = s.pluginOnDisconnect(pluginContext, err)
	})
	agentErr = s.listenAgentMessages(&pluginContext, &ag, stream)
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
				return fmt.Errorf("agent %v disconnected, end-of-file stream", ag.Name)
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

func (s *Server) updateAgentStatus(agentMode string, a *agent.Agent) error {
	if agentMode == pb.AgentModeEmbeddedType {
		return nil
	}
	if _, err := s.AgentService.Persist(a); err != nil {
		sentry.CaptureException(err)
		log.Errorf("failed updating agent status, err=%v", err)
		return status.Errorf(codes.Internal, "internal error, failed updating agent status")
	}
	return nil
}

func (s *Server) configurationData(orgName string) []byte {
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
	return transportConfigBytes
}
