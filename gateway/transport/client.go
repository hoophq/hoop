package transport

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	pbclient "github.com/runopsio/hoop/common/proto/client"
	pbgateway "github.com/runopsio/hoop/common/proto/gateway"
	"github.com/runopsio/hoop/gateway/client"
	"github.com/runopsio/hoop/gateway/connection"
	"github.com/runopsio/hoop/gateway/plugin"
	rv "github.com/runopsio/hoop/gateway/review"
	justintime "github.com/runopsio/hoop/gateway/review/jit"
	"github.com/runopsio/hoop/gateway/session"
	pluginsrbac "github.com/runopsio/hoop/gateway/transport/plugins/accesscontrol"
	pluginsaudit "github.com/runopsio/hoop/gateway/transport/plugins/audit"
	pluginsdlp "github.com/runopsio/hoop/gateway/transport/plugins/dlp"
	pluginsindex "github.com/runopsio/hoop/gateway/transport/plugins/index"
	pluginsjit "github.com/runopsio/hoop/gateway/transport/plugins/jit"
	pluginsreview "github.com/runopsio/hoop/gateway/transport/plugins/review"
	"github.com/runopsio/hoop/gateway/user"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type (
	pluginConfig struct {
		Plugin
		config []string
	}

	Plugin interface {
		Name() string
		OnStartup(config plugin.Config) error
		OnConnect(p plugin.Config) error
		OnReceive(pluginConfig plugin.Config, config []string, packet *pb.Packet) error
		OnDisconnect(p plugin.Config, errMsg error) error
	}
)

var allPlugins []Plugin

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

func LoadPlugins(sessionStore *session.Storage, pluginStore *plugin.Storage, apiURL string) {
	allPlugins = []Plugin{
		pluginsaudit.New(),
		pluginsindex.New(sessionStore, pluginStore),
		pluginsreview.New(apiURL),
		pluginsjit.New(apiURL),
		pluginsdlp.New(),
		pluginsrbac.New(),
	}
}

func bindClient(sessionID string,
	stream pb.Transport_ConnectServer,
	connection *connection.Connection,
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

func setClientMetdata(into *client.Client, md metadata.MD) {
	into.Hostname = extractData(md, "hostname")
	into.MachineId = extractData(md, "machine-id")
	into.KernelVersion = extractData(md, "kernel-version")
	into.Version = extractData(md, "version")
	into.GoVersion = extractData(md, "go-version")
	into.Compiler = extractData(md, "compiler")
	into.Platform = extractData(md, "platform")
	into.Verb = extractData(md, "verb")
}

func (s *Server) subscribeClient(stream pb.Transport_ConnectServer, token string) error {
	ctx := stream.Context()
	md, _ := metadata.FromIncomingContext(ctx)

	hostname := extractData(md, "hostname")
	machineId := extractData(md, "machine-id")
	kernelVersion := extractData(md, "kernel-version")
	connectionName := extractData(md, "connection-name")
	clientVerb := extractData(md, "verb")
	clientOrigin := extractData(md, "origin")

	sub, err := s.exchangeUserToken(token)
	if err != nil {
		log.Debugf("failed verifying access token, reason=%v", err)
		return status.Errorf(codes.Unauthenticated, "invalid authentication")
	}

	context, err := s.UserService.FindBySub(sub)
	if err != nil || context.User == nil {
		return status.Errorf(codes.Unauthenticated, "invalid authentication")
	}

	conn, err := s.ConnectionService.FindOne(context, connectionName)
	if err != nil {
		sentry.CaptureException(err)
		return status.Errorf(codes.Internal, err.Error())
	}

	if conn == nil {
		return status.Errorf(codes.NotFound, fmt.Sprintf("connection '%v' not found", connectionName))
	}

	// When a session id is coming from the client,
	// it's not safe to rely on it. A validation is required
	// to maintain the integrity of the database.
	sessionID := extractData(md, "session-id")
	if sessionID != "" {
		if err := s.validateSessionID(sessionID); err != nil {
			return status.Errorf(codes.AlreadyExists, err.Error())
		}
	}
	if sessionID == "" {
		sessionID = uuid.NewString()
	}
	c := &client.Client{
		Id:           uuid.NewString(),
		SessionID:    sessionID,
		OrgId:        context.Org.Id,
		UserId:       context.User.Id,
		Status:       client.StatusConnected,
		ConnectionId: conn.Id,
		AgentId:      conn.AgentId,
	}
	s.trackSessionStatus(sessionID, pb.SessionPhaseClientConnect, nil)
	setClientMetdata(c, md)

	pConfig := plugin.Config{
		SessionId:      sessionID,
		ConnectionId:   conn.Id,
		ConnectionName: connectionName,
		ConnectionType: string(conn.Type),
		Org:            context.Org.Id,
		UserID:         context.User.Id,
		UserName:       context.User.Name,
		UserEmail:      context.User.Email,
		Verb:           clientVerb,
		Hostname:       hostname,
		MachineId:      machineId,
		KernelVersion:  kernelVersion,
		ParamsData:     map[string]any{"client": clientOrigin},
	}

	switch string(conn.Type) {
	case pb.ConnectionTypeCommandLine: // noop - this type can connect/exec
	default: // tcp, mysql, postgres
		if clientVerb == pb.ClientVerbExec {
			return status.Errorf(codes.InvalidArgument,
				fmt.Sprintf("exec is only allowed to command-line type connections. Use 'hoop connect %s' instead", conn.Name))
		}
	}

	s.startDisconnectClientSink(sessionID, clientOrigin, func(err error) {
		defer unbindClient(sessionID)
		c.Status = client.StatusDisconnected
		_, _ = s.ClientService.Persist(c)
		if stream := getAgentStream(c.AgentId); stream != nil {
			_ = stream.Send(&pb.Packet{
				Type: pbagent.SessionClose,
				Spec: map[string][]byte{
					pb.SpecGatewaySessionID: []byte(c.SessionID),
				},
			})
		}
		_ = s.pluginOnDisconnect(pConfig, err)
	})
	plugins, err := s.loadConnectPlugins(context, pConfig)
	bindClient(sessionID, stream, conn, plugins)
	if err != nil {
		// _ = s.pluginOnDisconnect(pConfig, err)
		s.disconnectClient(sessionID, err)
		s.trackSessionStatus(sessionID, pb.SessionPhaseClientErr, err)
		return status.Errorf(codes.FailedPrecondition, err.Error())
	}

	if _, err := s.ClientService.Persist(c); err != nil {
		log.With("session", sessionID).Errorf("failed saving client connection, err=%v", err)
		s.disconnectClient(sessionID, err)
		s.trackSessionStatus(sessionID, pb.SessionPhaseClientErr, fmt.Errorf("failed saving client connection, err=%v", err))
		sentry.CaptureException(err)
		return err
	}

	s.Analytics.Track(context.User.Id, clientVerb, map[string]any{
		"sessionID":       sessionID,
		"connection-name": connectionName,
		"connection-type": conn.Type,
		"hostname":        hostname,
		"machine-id":      machineId,
		"kernel-version":  kernelVersion,
		"client-version":  c.Version,
		"go-version":      c.GoVersion,
		"platform":        c.Platform,
	})

	log.With("session", sessionID).
		Infof("proxy connected: user=%v,hostname=%v,origin=%v,verb=%v,platform=%v,version=%v,goversion=%v,compiler=%v",
			pConfig.UserEmail, c.Hostname, clientOrigin, c.Verb, c.Platform, c.Version, c.GoVersion, c.Compiler)
	s.trackSessionStatus(sessionID, pb.SessionPhaseClientConnected, nil)
	clientErr := s.listenClientMessages(stream, c, conn, pConfig)
	if status, ok := status.FromError(clientErr); ok && status.Code() == codes.Canceled {
		log.With("session", c.SessionID, "origin", clientOrigin).Infof("grpc client connection canceled")
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

func (s *Server) listenClientMessages(
	stream pb.Transport_ConnectServer,
	c *client.Client,
	conn *connection.Connection,
	config plugin.Config) error {

	ctx := stream.Context()
	c.Context = ctx

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-c.Context.Done():
			return fmt.Errorf("session ended, reached connection duration")
		default:
		}

		// receive data from stream
		pkt, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				log.With("session", c.SessionID).Debugf("EOF")
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
		pkt.Spec[pb.SpecGatewaySessionID] = []byte(c.SessionID)

		log.With("session", c.SessionID).Debugf("receive client packet type [%s]", pkt.Type)
		if err := s.pluginOnReceive(config, pkt); err != nil {
			log.With("session", c.SessionID).Errorf("plugin rejected packet, err=%v", err)
			sentry.CaptureException(err)
			return status.Errorf(codes.Internal, "internal error, packet rejected, contact the administrator")
		}
		err = s.processClientPacket(pkt, config.Verb, c, conn)
		if err != nil {
			log.With("session", c.SessionID).Warnf("failed processing client packet, err=%v", err)
			return status.Errorf(codes.FailedPrecondition, err.Error())
		}
	}
}

func (s *Server) processClientPacket(pkt *pb.Packet, clientVerb string, client *client.Client, conn *connection.Connection) error {
	switch pb.PacketType(pkt.Type) {
	case pbagent.SessionOpen:
		switch clientVerb {
		case pb.ClientVerbConnect:
			s.trackSessionStatus(client.SessionID, pb.SessionPhaseClientSessionOpen, nil)
			return s.processSessionOpenConnect(pkt, client, conn)
		case pb.ClientVerbExec:
			s.trackSessionStatus(client.SessionID, pb.SessionPhaseClientSessionOpen, nil)
			return s.processSessionOpenExec(pkt, client, conn)
		default:
			return fmt.Errorf("unknown connection type '%v'", conn.Type)
		}
	default:
		agentStream := getAgentStream(conn.AgentId)
		if agentStream == nil {
			return fmt.Errorf("agent not found for connection %s", conn.Name)
		}
		_ = agentStream.Send(pkt)
	}
	return nil
}

func (s *Server) processSessionOpenConnect(pkt *pb.Packet, client *client.Client, conn *connection.Connection) error {
	spec := map[string][]byte{
		pb.SpecGatewaySessionID: []byte(client.SessionID),
		pb.SpecConnectionType:   []byte(conn.Type),
	}

	jitStatus, ok := pkt.Spec[pb.SpecJitStatus]
	if ok {
		switch justintime.Status(jitStatus) {
		case justintime.StatusApproved:
			connectDuration, err := time.ParseDuration(string(pkt.Spec[pb.SpecJitTimeout]))
			if err != nil {
				return fmt.Errorf("wrong connect duration input, found=%#v", string(pkt.Spec[pb.SpecJitTimeout]))
			}
			ctx, _ := context.WithTimeout(client.Context, connectDuration)
			client.Context = ctx
		default:
			jitID := pkt.Spec[pb.SpecGatewayJitID]
			clientStream := getClientStream(client.SessionID)
			_ = clientStream.Send(&pb.Packet{
				Type:    pbclient.SessionOpenWaitingApproval,
				Payload: []byte(fmt.Sprintf("%s/plugins/jits/%s", s.IDProvider.ApiURL, jitID)),
			})
			return nil
		}
	}

	agentStream := getAgentStream(conn.AgentId)
	if agentStream == nil {
		clientStream := getClientStream(client.SessionID)
		_ = clientStream.Send(&pb.Packet{
			Type: pbclient.SessionOpenAgentOffline,
			Spec: spec,
		})
		return fmt.Errorf("agent is offline")
	}

	if s.GcpDLPRawCredentials != "" {
		spec[pb.SpecAgentGCPRawCredentialsKey] = []byte(s.GcpDLPRawCredentials)
	}
	connParams, err := s.addConnectionParams(pkt, conn, client)
	if err != nil {
		sentry.CaptureException(err)
		return err
	}
	spec[pb.SpecAgentConnectionParamsKey] = connParams
	_ = agentStream.Send(&pb.Packet{
		Type: pbagent.SessionOpen,
		Spec: spec,
	})
	return nil
}

func (s *Server) processSessionOpenExec(pkt *pb.Packet, client *client.Client, conn *connection.Connection) error {
	spec := map[string][]byte{
		pb.SpecGatewaySessionID: []byte(client.SessionID),
		pb.SpecConnectionType:   []byte(conn.Type),
	}

	existingReviewData := pkt.Spec[pb.SpecReviewDataKey]
	if existingReviewData != nil {
		var review rv.Review
		if err := pb.GobDecodeInto(existingReviewData, &review); err != nil {
			sentry.CaptureException(err)
			return err
		}
		if !(review.Status == rv.StatusApproved || review.Status == rv.StatusProcessing) {
			clientStream := getClientStream(client.SessionID)
			_ = clientStream.Send(&pb.Packet{
				Type:    pbclient.SessionOpenWaitingApproval,
				Payload: []byte(fmt.Sprintf("%s/plugins/reviews/%s", s.IDProvider.ApiURL, review.Id)),
			})
			return nil
		}
		ctx := &user.Context{
			Org:  &user.Org{Id: client.OrgId},
			User: &user.User{Id: client.UserId},
		}
		if review.Status == rv.StatusApproved {
			review.Status = rv.StatusProcessing
			if err := s.ReviewService.Persist(ctx, &review); err != nil {
				log.Errorf("failed to update review status, err=%v", err)
				return fmt.Errorf("failed to execute approved review")
			}
		}
	}

	if s.GcpDLPRawCredentials != "" {
		spec[pb.SpecAgentGCPRawCredentialsKey] = []byte(s.GcpDLPRawCredentials)
	}

	agentStream := getAgentStream(conn.AgentId)
	if agentStream == nil {
		spec[pb.SpecClientExecArgsKey] = pkt.Spec[pb.SpecClientExecArgsKey]
		clientStream := getClientStream(client.SessionID)
		_ = clientStream.Send(&pb.Packet{
			Type: pbclient.SessionOpenAgentOffline,
			Spec: spec,
		})
		return fmt.Errorf("agent is offline")
	}

	connParams, err := s.addConnectionParams(pkt, conn, client)
	if err != nil {
		sentry.CaptureException(err)
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
	_ = agentStream.Send(&pb.Packet{
		Type: pbagent.SessionOpen,
		Spec: spec,
	})
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
		if p.Plugin.Name() == pluginsdlp.Name {
			infoTypes = p.config
		}
	}
	return infoTypes
}

func (s *Server) addConnectionParams(pkt *pb.Packet, conn *connection.Connection, client *client.Client) ([]byte, error) {
	clientArgs := clientArgsDecode(pkt.Spec)
	infoTypes := getInfoTypes(client.SessionID)

	ctx := &user.Context{Org: &user.Org{Id: client.OrgId}}
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
		for _, connectionName := range pluginItem.Connections {
			if conn.Name == connectionName {
				pluginName = pluginItem.Name
				break
			}
		}
		if pluginName == "" {
			continue
		}
		pl, err := s.PluginService.FindOne(ctx, pluginName)
		if err != nil {
			return nil, fmt.Errorf("failed loading plugin for connection (%v), err=%v", conn.Name, err)
		}

		for _, plConn := range pl.Connections {
			if plConn.Name != conn.Name {
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
		ConnectionName: conn.Name,
		ConnectionType: string(conn.Type),
		UserID:         client.UserId,
		EnvVars:        conn.Secret,
		CmdList:        conn.Command,
		ClientArgs:     clientArgs,
		ClientVerb:     client.Verb,
		DLPInfoTypes:   infoTypes,
		PluginHookList: pluginHookList,
	})
	if err != nil {
		return nil, fmt.Errorf("failed encoding connection params err=%v", err)
	}

	return encConnectionParams, nil
}

func (s *Server) ReviewStatusChange(sessionID string, status rv.Status, command []byte) error {
	clientStream := getClientStream(sessionID)
	if clientStream != nil {
		payload := command
		packetType := pbclient.SessionOpenApproveOK
		if status == rv.StatusRejected {
			packetType = pbclient.SessionClose
			payload = []byte(`access to connection has been denied`)
			s.disconnectClient(sessionID, fmt.Errorf("access to connection has been denied"))
		}
		_ = clientStream.Send(&pb.Packet{
			Type:    packetType,
			Spec:    map[string][]byte{pb.SpecGatewaySessionID: []byte(sessionID)},
			Payload: payload,
		})
	}
	return nil
}

func (s *Server) JitStatusChange(sessionID string, status justintime.Status) error {
	clientStream := getClientStream(sessionID)
	if clientStream != nil {
		var payload []byte
		packetType := pbclient.SessionOpenApproveOK
		if status == justintime.StatusRejected {
			packetType = pbclient.SessionClose
			payload = []byte(`access to connection has been denied`)
			s.disconnectClient(sessionID, fmt.Errorf("access to connection has been denied"))
		}
		_ = clientStream.Send(&pb.Packet{
			Type:    packetType,
			Spec:    map[string][]byte{pb.SpecGatewaySessionID: []byte(sessionID)},
			Payload: payload,
		})
	}
	return nil
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

func (s *Server) loadConnectPlugins(ctx *user.Context, config plugin.Config) ([]pluginConfig, error) {
	pluginsConfig := make([]pluginConfig, 0)
	var nonRegisteredPlugins []string
	for _, p := range allPlugins {
		p1, err := s.PluginService.FindOne(ctx, p.Name())
		if err != nil {
			log.Errorf("failed retrieving plugin %q, err=%v", p.Name(), err)
			return nil, status.Errorf(codes.Internal, "failed registering plugins")
		}
		if p1 == nil {
			nonRegisteredPlugins = append(nonRegisteredPlugins, p.Name())
			continue
		}

		if p.Name() == pluginsaudit.Name {
			config.ParamsData[pluginsaudit.StorageWriterParam] = s.SessionService.Storage.NewGenericStorageWriter()
		}

		if p.Name() == pluginsreview.Name {
			config.ParamsData[pluginsreview.ReviewServiceParam] = &s.ReviewService
			config.ParamsData[pluginsreview.UserServiceParam] = &s.UserService
			config.ParamsData[pluginsreview.NotificationServiceParam] = s.NotificationService
		}

		if p.Name() == pluginsjit.Name {
			config.ParamsData[pluginsjit.JitServiceParam] = &s.JitService
			config.ParamsData[pluginsjit.UserServiceParam] = &s.UserService
			config.ParamsData[pluginsjit.NotificationServiceParam] = s.NotificationService
		}

		for _, c := range p1.Connections {
			if c.Name == config.ConnectionName {
				cfg := c.Config
				if len(ctx.User.Groups) > 0 && len(c.Groups) > 0 {
					cfg = make([]string, 0)
					for _, u := range ctx.User.Groups {
						cfg = append(cfg, c.Groups[u]...)
					}
					if len(cfg) == 0 {
						cfg = c.Config
					}
				}
				cfg = removeDuplicates(cfg)
				ep := pluginConfig{
					Plugin: p,
					config: cfg,
				}

				if err := p.OnStartup(config); err != nil {
					log.Errorf("failed starting plugin %q, err=%v", p.Name(), err)
					return pluginsConfig, status.Errorf(codes.Internal, "failed starting plugin")
				}

				if err = p.OnConnect(config); err != nil {
					log.Warnf("plugin %q refused to accept connection %q, err=%v", p1.Name, config.SessionId, err)
					return pluginsConfig, status.Errorf(codes.FailedPrecondition, err.Error())
				}

				pluginsConfig = append(pluginsConfig, ep)
				break
			}
		}
	}
	if len(nonRegisteredPlugins) > 0 {
		log.With("session", config.SessionId).Infof("non registered plugins %v", nonRegisteredPlugins)
	}
	return pluginsConfig, nil
}

func (s *Server) pluginOnDisconnect(config plugin.Config, errMsg error) error {
	plugins := getPlugins(config.SessionId)
	for _, p := range plugins {
		return p.OnDisconnect(config, errMsg)
	}
	return nil
}

func (s *Server) pluginOnReceive(config plugin.Config, pkt *pb.Packet) error {
	plugins := getPlugins(config.SessionId)
	for _, p := range plugins {
		if err := p.OnReceive(config, p.config, pkt); err != nil {
			return err
		}
	}
	return nil
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
