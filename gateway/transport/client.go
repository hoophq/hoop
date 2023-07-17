package transport

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/getsentry/sentry-go"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	pbclient "github.com/runopsio/hoop/common/proto/client"
	pbgateway "github.com/runopsio/hoop/common/proto/gateway"
	"github.com/runopsio/hoop/gateway/analytics"
	apiconnectionapps "github.com/runopsio/hoop/gateway/api/connectionapps"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	pluginsslack "github.com/runopsio/hoop/gateway/transport/plugins/slack"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
	"github.com/runopsio/hoop/gateway/user"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type (
	pluginConfig struct {
		plugintypes.Plugin
		config []string
	}
)

var cc = struct {
	clients map[string]pb.Transport_ConnectServer
	plugins map[string][]pluginConfig
	mu      sync.Mutex
}{
	clients: make(map[string]pb.Transport_ConnectServer),
	plugins: make(map[string][]pluginConfig),
	mu:      sync.Mutex{},
}

var disconnectSink = struct {
	items map[string]chan error
	mu    sync.Mutex
}{
	items: make(map[string]chan error),
	mu:    sync.Mutex{},
}

func bindClient(sessionID string,
	stream pb.Transport_ConnectServer,
	pluginsConfig []pluginConfig) {

	cc.mu.Lock()
	defer cc.mu.Unlock()
	cc.clients[sessionID] = stream
	cc.plugins[sessionID] = pluginsConfig
}

func unbindClient(sessionID string) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	delete(cc.clients, sessionID)
	delete(cc.plugins, sessionID)
}

func getClientStream(id string) pb.Transport_ConnectServer {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	return cc.clients[id]
}

func getPlugins(sessionID string) []pluginConfig {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	return cc.plugins[sessionID]
}

func (s *Server) subscribeClient(stream pb.Transport_ConnectServer, token string) error {
	ctx := stream.Context()
	md, _ := metadata.FromIncomingContext(ctx)

	hostname := mdget(md, "hostname")
	connectionName := mdget(md, "connection-name")
	clientVerb := mdget(md, "verb")
	clientOrigin := mdget(md, "origin")

	sub, err := s.exchangeUserToken(token)
	if err != nil {
		log.Debugf("failed verifying access token, reason=%v", err)
		return status.Errorf(codes.Unauthenticated, "invalid authentication")
	}

	userCtx, err := s.UserService.FindBySub(sub)
	if err != nil || userCtx.User == nil {
		return status.Errorf(codes.Unauthenticated, "invalid authentication")
	}

	conn, err := s.ConnectionService.FindOne(userCtx, connectionName)
	if err != nil {
		sentry.CaptureException(err)
		return status.Errorf(codes.Internal, err.Error())
	}

	if conn == nil {
		return status.Errorf(codes.NotFound, fmt.Sprintf("connection '%v' not found", connectionName))
	}

	// it's an sidecar agent connection, request agent to connect
	if conn.AgentId == conn.Name && !hasAgentStream(conn.AgentId) {
		log.With("user", userCtx.User.Email).Infof("requesting connection with remote agent %s", connectionName)
		err = apiconnectionapps.RequestGrpcConnection(connectionName, hasAgentStream)
		if err != nil {
			log.Warnf("%v %v", err, connectionName)
			return status.Errorf(codes.Aborted, err.Error())
		}
		log.Infof("found the remote agent for %v", connectionName)
	}

	// When a session id is coming from the client,
	// it's not safe to rely on it. A validation is required
	// to maintain the integrity of the database.
	sessionID := mdget(md, "session-id")

	sessionScript := ""
	sessionLabels := map[string]string{}

	if sessionID != "" {
		session, err := s.SessionService.FindOne(userCtx, sessionID)
		if err != nil {
			log.Errorf("Failed getting the session, err=%v", err)
			sentry.CaptureException(err)
			return status.Errorf(codes.Internal, "It was a problem finding the session")
		}
		if session != nil {
			sessionScript = session.Script["data"]
			sessionLabels = session.Labels
		}
	}

	if sessionID == "" {
		sessionID = uuid.NewString()
	}

	s.trackSessionStatus(sessionID, pb.SessionPhaseClientConnect, nil)

	pluginContext := plugintypes.Context{
		Context: context.Background(),
		SID:     sessionID,

		OrgID:      userCtx.Org.Id,
		UserID:     userCtx.User.Id,
		UserName:   userCtx.User.Name,
		UserEmail:  userCtx.User.Email,
		UserGroups: userCtx.User.Groups,

		ConnectionID:      conn.Id,
		ConnectionName:    conn.Name,
		ConnectionType:    fmt.Sprintf("%v", conn.Type),
		ConnectionCommand: conn.Command,
		ConnectionSecret:  conn.Secret,
		ConnectionAgentID: conn.AgentId,

		ClientVerb:   clientVerb,
		ClientOrigin: clientOrigin,

		Script: sessionScript,
		Labels: sessionLabels,

		ParamsData: map[string]any{},
	}

	if err := pluginContext.Validate(); err != nil {
		log.Errorf("failed validating plugin context, err=%v", err)
		sentry.CaptureException(err)
		return status.Errorf(codes.Internal,
			"failed validating connection context, contact the administrator")
	}

	switch string(conn.Type) {
	case pb.ConnectionTypeCommandLine: // noop - this type can connect/exec
	case pb.ConnectionTypeTCP:
		if clientVerb == pb.ClientVerbExec {
			return status.Errorf(codes.InvalidArgument,
				fmt.Sprintf("exec is not allowed for tcp type connections. Use 'hoop connect %s' instead", conn.Name))
		}
	}

	s.startDisconnectClientSink(sessionID, clientOrigin, func(err error) {
		defer unbindClient(sessionID)
		if stream := getAgentStream(conn.AgentId); stream != nil {
			_ = stream.Send(&pb.Packet{
				Type: pbagent.SessionClose,
				Spec: map[string][]byte{
					pb.SpecGatewaySessionID: []byte(sessionID),
				},
			})
		}
		_ = s.pluginOnDisconnect(pluginContext, err)
	})
	plugins, err := s.loadConnectPlugins(userCtx, pluginContext)
	bindClient(sessionID, stream, plugins)
	if err != nil {
		s.disconnectClient(sessionID, err)
		s.trackSessionStatus(sessionID, pb.SessionPhaseClientErr, err)
		return status.Errorf(codes.FailedPrecondition, err.Error())
	}
	eventName := analytics.EventGrpcExec
	if clientVerb == pb.ClientVerbConnect {
		eventName = analytics.EventGrpcConnect
	}
	s.Analytics.Track(userCtx.ToAPIContext(), eventName, map[string]any{
		"session-id":      sessionID,
		"connection-name": connectionName,
		"connection-type": conn.Type,
		"client-version":  mdget(md, "version"),
		"go-version":      mdget(md, "go-version"),
		"platform":        mdget(md, "platform"),
		"hostname":        hostname,
		"user-agent":      mdget(md, "user-agent"),
		"origin":          clientOrigin,
		"verb":            clientVerb,
	})

	log.With("session", sessionID).
		Infof("proxy connected: user=%v,hostname=%v,origin=%v,verb=%v,platform=%v,version=%v,goversion=%v",
			userCtx.User.Email, mdget(md, "hostname"), clientOrigin, clientVerb,
			mdget(md, "platform"), mdget(md, "version"), mdget(md, "goversion"))
	s.trackSessionStatus(sessionID, pb.SessionPhaseClientConnected, nil)
	clientErr := s.listenClientMessages(stream, pluginContext)
	if status, ok := status.FromError(clientErr); ok && status.Code() == codes.Canceled {
		log.With("session", sessionID, "origin", clientOrigin).Infof("grpc client connection canceled")
		// it means the api client has disconnected,
		// it will let the session open to receive packets
		// until a session close packet is received or the
		// agent is disconnected
		if clientOrigin == pb.ConnectionOriginClientAPI {
			clientErr = nil
		}
	}
	defer s.disconnectClient(sessionID, clientErr)
	if clientErr != nil {
		s.trackSessionStatus(sessionID, pb.SessionPhaseClientErr, clientErr)
		return clientErr
	}
	s.trackSessionStatus(sessionID, pb.SessionPhaseGatewaySessionClose, clientErr)
	return clientErr
}

func (s *Server) listenClientMessages(stream pb.Transport_ConnectServer, pctx plugintypes.Context) error {
	for {
		select {
		case <-stream.Context().Done():
			return nil
		case <-pctx.Context.Done():
			return status.Error(codes.Aborted, "session ended, reached connection duration")
		default:
		}

		// receive data from stream
		pkt, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				log.With("session", pctx.SID).Debugf("EOF")
				return err
			}
			if status, ok := status.FromError(err); ok && status.Code() == codes.Canceled {
				return err
			}
			log.Warnf("received error from client, err=%v", err)
			sentry.CaptureException(err)
			return status.Errorf(codes.Internal, "internal error, failed receiving client packet")
		}
		// skip old/new clients
		if pkt.Type == pbgateway.KeepAlive || pkt.Type == "KeepAlive" {
			continue
		}
		if pkt.Spec == nil {
			pkt.Spec = make(map[string][]byte)
		}
		pkt.Spec[pb.SpecGatewaySessionID] = []byte(pctx.SID)
		log.With("session", pctx.SID).Debugf("receive client packet type [%s]", pkt.Type)
		shouldProcessClientPacket := true
		connectResponse, err := s.pluginOnReceive(pctx, pkt)
		switch v := err.(type) {
		case *plugintypes.InternalError:
			if v.HasInternalErr() {
				log.With("session", pctx.SID).Errorf("plugin rejected packet, %v", v.FullErr())
				sentry.CaptureException(fmt.Errorf(v.FullErr()))
			}
			return status.Errorf(codes.Internal, err.Error())
		case nil: // noop
		default:
			return status.Errorf(codes.Internal, err.Error())
		}
		if connectResponse != nil {
			if connectResponse.Context != nil {
				pctx.Context = connectResponse.Context
			}
			if cs := getClientStream(pctx.SID); cs != nil && connectResponse.ClientPacket != nil {
				_ = cs.Send(connectResponse.ClientPacket)
				shouldProcessClientPacket = false
			}
		}
		if shouldProcessClientPacket {
			err = s.processClientPacket(pkt, pctx)
			if err != nil {
				log.With("session", pctx.SID).Warnf("failed processing client packet, err=%v", err)
				return status.Errorf(codes.FailedPrecondition, err.Error())
			}
		}
	}
}

func (s *Server) processClientPacket(pkt *pb.Packet, pctx plugintypes.Context) error {
	switch pb.PacketType(pkt.Type) {
	case pbagent.SessionOpen:
		return s.processSessionOpenPacket(pkt, pctx)
	default:
		agentStream := getAgentStream(pctx.ConnectionAgentID)
		if agentStream == nil {
			return fmt.Errorf("agent not found for connection %s", pctx.ConnectionName)
		}
		_ = agentStream.Send(pkt)
	}
	return nil
}

func (s *Server) processSessionOpenPacket(pkt *pb.Packet, pctx plugintypes.Context) error {
	spec := map[string][]byte{
		pb.SpecGatewaySessionID: []byte(pctx.SID),
		pb.SpecConnectionType:   []byte(pctx.ConnectionType),
	}

	if s.GcpDLPRawCredentials != "" {
		spec[pb.SpecAgentGCPRawCredentialsKey] = []byte(s.GcpDLPRawCredentials)
	}

	agentStream := getAgentStream(pctx.ConnectionAgentID)
	if agentStream == nil {
		spec[pb.SpecClientExecArgsKey] = pkt.Spec[pb.SpecClientExecArgsKey]
		clientStream := getClientStream(pctx.SID)
		_ = clientStream.Send(&pb.Packet{
			Type: pbclient.SessionOpenAgentOffline,
			Spec: spec,
		})
		return pb.ErrAgentOffline
	}

	clientArgs := clientArgsDecode(pkt.Spec)
	connParams, err := s.addConnectionParams(clientArgs, pctx)
	if err != nil {
		return err
	}
	spec[pb.SpecAgentConnectionParamsKey] = connParams
	// Propagate client spec.
	// Do not allow replacing system ones
	for key, val := range pkt.Spec {
		if _, ok := spec[key]; ok {
			continue
		}
		spec[key] = val
	}
	_ = agentStream.Send(&pb.Packet{Type: pbagent.SessionOpen, Spec: spec})
	return nil
}

func clientArgsDecode(spec map[string][]byte) []string {
	var clientArgs []string
	if spec != nil {
		encArgs := spec[pb.SpecClientExecArgsKey]
		if len(encArgs) > 0 {
			if err := pb.GobDecodeInto(encArgs, &clientArgs); err != nil {
				log.Printf("failed decoding args, err=%v", err)
			}
		}
	}
	return clientArgs
}

func getInfoTypes(sessionID string) []string {
	var infoTypes []string
	for _, p := range getPlugins(sessionID) {
		if p.Plugin.Name() == plugintypes.PluginDLPName {
			infoTypes = p.config
		}
	}
	return infoTypes
}

func (s *Server) addConnectionParams(clientArgs []string, pctx plugintypes.Context) ([]byte, error) {
	infoTypes := getInfoTypes(pctx.SID)

	ctx := &user.Context{Org: &user.Org{Id: pctx.OrgID}}
	plugins, err := s.PluginService.FindAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed loading plugin hooks, err=%v", err)
	}
	var pluginHookList []map[string]any
	for _, pluginItem := range plugins {
		if pluginItem.Source == nil {
			continue
		}
		var pluginName string
		for _, conn := range pluginItem.Connections {
			if pctx.ConnectionName == conn.Name {
				pluginName = pluginItem.Name
				break
			}
		}
		if pluginName == "" {
			continue
		}
		pl, err := s.PluginService.FindOne(ctx, pluginName)
		if err != nil {
			return nil, fmt.Errorf("failed loading plugin for connection (%v), err=%v", pctx.ConnectionName, err)
		}

		for _, plConn := range pl.Connections {
			if plConn.Name != pctx.ConnectionName {
				continue
			}
			// TODO: connection config should change in the future to accept
			// a map instead of a list. For now, the first record is used
			// as the configuration encoded as base64 + json
			var connectionConfigB64JSONEnc string
			if len(plConn.Config) > 0 {
				connectionConfigB64JSONEnc = plConn.Config[0]
			}
			var pluginEnvVars map[string]string
			if pl.Config != nil {
				pluginEnvVars = pl.Config.EnvVars
			}
			pluginHookList = append(pluginHookList, map[string]any{
				"plugin_registry":   s.PluginRegistryURL,
				"plugin_name":       pl.Name,
				"plugin_source":     *pl.Source,
				"plugin_envvars":    pluginEnvVars,
				"connection_config": map[string]any{"jsonb64": connectionConfigB64JSONEnc},
			})
			// load the plugin once per connection
			break
		}
	}
	encConnectionParams, err := pb.GobEncode(&pb.AgentConnectionParams{
		ConnectionName: pctx.ConnectionName,
		ConnectionType: pctx.ConnectionType,
		UserID:         pctx.UserID,
		EnvVars:        pctx.ConnectionSecret,
		CmdList:        pctx.ConnectionCommand,
		ClientArgs:     clientArgs,
		ClientVerb:     pctx.ClientVerb,
		DLPInfoTypes:   infoTypes,
		PluginHookList: pluginHookList,
	})
	if err != nil {
		return nil, fmt.Errorf("failed encoding connection params err=%v", err)
	}

	return encConnectionParams, nil
}

func (s *Server) ReviewStatusChange(ctx *user.Context, rev *types.Review) {
	pluginsslack.SendApprovedMessage(ctx, rev)
	if clientStream := getClientStream(rev.Session); clientStream != nil {
		payload := []byte(rev.Input)
		packetType := pbclient.SessionOpenApproveOK
		if rev.Status == types.ReviewStatusRejected {
			packetType = pbclient.SessionClose
			payload = []byte(`access to connection has been denied`)
			s.disconnectClient(rev.Session, fmt.Errorf("access to connection has been denied"))
		}
		_ = clientStream.Send(&pb.Packet{
			Type:    packetType,
			Spec:    map[string][]byte{pb.SpecGatewaySessionID: []byte(rev.Session)},
			Payload: payload,
		})
	}
}

func (s *Server) exchangeUserToken(token string) (string, error) {
	if s.Profile == pb.DevProfile {
		return "test-user", nil
	}

	sub, err := s.IDProvider.VerifyAccessToken(token)
	if err != nil {
		return "", err
	}

	return sub, nil
}

func (s *Server) loadConnectPlugins(ctx *user.Context, pctx plugintypes.Context) ([]pluginConfig, error) {
	pluginsConfig := make([]pluginConfig, 0)
	var nonRegisteredPlugins []string
	for _, p := range s.RegisteredPlugins {
		p1, err := s.PluginService.FindOne(ctx, p.Name())
		if err != nil {
			log.Errorf("failed retrieving plugin %q, err=%v", p.Name(), err)
			return nil, status.Errorf(codes.Internal, "failed registering plugins")
		}
		if p1 == nil {
			nonRegisteredPlugins = append(nonRegisteredPlugins, p.Name())
			continue
		}

		if p.Name() == plugintypes.PluginSlackName {
			if p1.Config != nil {
				pctx.ParamsData[pluginsslack.PluginConfigEnvVarsParam] = p1.Config.EnvVars
			}
		}

		for _, c := range p1.Connections {
			if c.Name == pctx.ConnectionName {
				cfg := removeDuplicates(c.Config)
				ep := pluginConfig{
					Plugin: p,
					config: cfg,
				}

				if err = p.OnConnect(pctx); err != nil {
					log.Warnf("plugin %q refused to accept connection %q, err=%v", p1.Name, pctx.SID, err)
					return pluginsConfig, status.Errorf(codes.FailedPrecondition, err.Error())
				}

				pluginsConfig = append(pluginsConfig, ep)
				break
			}
		}
	}
	if len(nonRegisteredPlugins) > 0 {
		log.With("session", pctx.SID).Infof("non registered plugins %v", nonRegisteredPlugins)
	}
	return pluginsConfig, nil
}

func (s *Server) pluginOnDisconnect(pctx plugintypes.Context, errMsg error) error {
	for _, p := range getPlugins(pctx.SID) {
		if err := p.OnDisconnect(pctx, errMsg); err != nil {
			return err
		}
	}
	return nil
}

// pluginOnReceive will process the OnReceive phase for every registered plugin.
// the response must follow the instructions contained in the *plugintypes.ConnectResponse object.
func (s *Server) pluginOnReceive(pctx plugintypes.Context, pkt *pb.Packet) (*plugintypes.ConnectResponse, error) {
	var response *plugintypes.ConnectResponse
	for _, p := range getPlugins(pctx.SID) {
		pctx.PluginConnectionConfig = p.config
		resp, err := p.OnReceive(pctx, pkt)
		if err != nil {
			return nil, err
		}
		if resp != nil && response == nil {
			response = resp
		}
	}
	return response, nil
}

func removeDuplicates(strSlice []string) []string {
	allKeys := make(map[string]bool)
	list := make([]string, 0)
	for _, item := range strSlice {
		if _, value := allKeys[item]; !value {
			allKeys[item] = true
			list = append(list, item)
		}
	}
	return list
}
