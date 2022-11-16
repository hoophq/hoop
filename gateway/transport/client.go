package transport

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/runopsio/hoop/gateway/plugin"
	pluginsaudit "github.com/runopsio/hoop/gateway/transport/plugins/audit"
	pluginsdlp "github.com/runopsio/hoop/gateway/transport/plugins/dlp"
	pluginsreview "github.com/runopsio/hoop/gateway/transport/plugins/review"
	"github.com/runopsio/hoop/gateway/user"

	"github.com/google/uuid"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/client"
	"github.com/runopsio/hoop/gateway/connection"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type (
	connectedClients struct {
		clients     map[string]pb.Transport_ConnectServer
		connections map[string]*connection.Connection
		plugins     map[string][]pluginConfig
		mu          sync.Mutex
	}

	pluginConfig struct {
		Plugin
		config []string
	}

	Plugin interface {
		Name() string
		OnStartup(config plugin.Config) error
		OnConnect(p plugin.Config) error
		OnReceive(pluginConfig plugin.Config, config []string, packet *pb.Packet) error
		OnDisconnect(p plugin.Config) error
	}
)

var allPlugins []Plugin

var cc = connectedClients{
	clients:     make(map[string]pb.Transport_ConnectServer),
	connections: make(map[string]*connection.Connection),
	plugins:     make(map[string][]pluginConfig),
	mu:          sync.Mutex{},
}

func LoadPlugins() {
	allPlugins = []Plugin{
		pluginsaudit.New(),
		pluginsreview.New(),
		pluginsdlp.New(),
	}
}

func bindClient(sessionID string,
	stream pb.Transport_ConnectServer,
	connection *connection.Connection,
	pluginsConfig []pluginConfig) {

	cc.mu.Lock()
	defer cc.mu.Unlock()

	cc.clients[sessionID] = stream
	cc.connections[sessionID] = connection
	cc.plugins[sessionID] = pluginsConfig
}

func unbindClient(id string) {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	delete(cc.clients, id)
	delete(cc.connections, id)
	delete(cc.plugins, id)
}

func getClientStream(id string) pb.Transport_ConnectServer {
	return cc.clients[id]
}

func getPlugins(id string) []pluginConfig {
	return cc.plugins[id]
}

func (s *Server) subscribeClient(stream pb.Transport_ConnectServer, token string) error {
	ctx := stream.Context()
	md, _ := metadata.FromIncomingContext(ctx)

	hostname := extractData(md, "hostname")
	machineId := extractData(md, "machine_id")
	kernelVersion := extractData(md, "kernel_version")
	connectionName := extractData(md, "connection_name")

	sub, err := s.exchangeUserToken(token)
	if err != nil {
		return status.Errorf(codes.Unauthenticated, "invalid authentication")
	}

	context, err := s.UserService.FindBySub(sub)
	if err != nil || context.User == nil {
		return status.Errorf(codes.Unauthenticated, "invalid authentication")
	}

	conn, err := s.ConnectionService.FindOne(context, connectionName)
	if err != nil {
		return status.Errorf(codes.Internal, err.Error())
	}

	if conn == nil {
		return status.Errorf(codes.NotFound, fmt.Sprintf("connection '%v' not found", connectionName))
	}

	sessionID := uuid.NewString()
	c := &client.Client{
		Id:            uuid.NewString(),
		SessionID:     sessionID,
		OrgId:         context.Org.Id,
		UserId:        context.User.Id,
		Hostname:      hostname,
		MachineId:     machineId,
		KernelVersion: kernelVersion,
		Status:        client.StatusConnected,
		ConnectionId:  conn.Id,
		AgentId:       conn.AgentId,
	}

	pConfig := plugin.Config{
		SessionId:      sessionID,
		ConnectionId:   conn.Id,
		ConnectionName: connectionName,
		ConnectionType: string(conn.Type),
		Org:            context.Org.Id,
		User:           context.User.Id,
		Hostname:       hostname,
		MachineId:      machineId,
		KernelVersion:  kernelVersion,
		ParamsData:     make(map[string]any),
	}

	plugins, err := s.loadConnectPlugins(context, pConfig)
	if err != nil {
		return status.Errorf(codes.FailedPrecondition, err.Error())
	}

	s.ClientService.Persist(c)
	bindClient(c.SessionID, stream, conn, plugins)

	s.clientGracefulShutdown(c)

	log.Printf("successful connection hostname: [%s], machineId [%s], kernelVersion [%s]", hostname, machineId, kernelVersion)
	clientErr := s.listenClientMessages(stream, c, conn, pConfig)

	if err := s.pluginOnDisconnect(pConfig); err != nil {
		log.Printf("session=%v ua=client - failed processing plugin on-disconnect phase, err=%v", sessionID, err)
	}

	s.disconnectClient(c)
	return clientErr
}

func (s *Server) listenClientMessages(
	stream pb.Transport_ConnectServer,
	c *client.Client,
	conn *connection.Connection,
	config plugin.Config) error {

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
				log.Printf("session=%v - client disconnected", c.SessionID)
				return nil
			}
			log.Printf("received error from client, err=%v", err)
			return status.Errorf(codes.Internal, "internal error, failed receiving client packet")
		}
		if pb.PacketType(pkt.Type) == pb.PacketKeepAliveType {
			continue
		}
		if pkt.Spec == nil {
			pkt.Spec = make(map[string][]byte)
		}
		pkt.Spec[pb.SpecGatewaySessionID] = []byte(c.SessionID)
		agStream := getAgentStream(conn.AgentId)
		if agStream == nil {
			log.Printf("agent connection not found for %q", c.AgentId)
			return status.Errorf(codes.FailedPrecondition, fmt.Sprintf("agent not found for %v", c.AgentId))
		}
		log.Printf("receive client packet type [%s] and session id [%s]", pkt.Type, c.SessionID)
		if err := s.pluginOnReceive(config, pkt); err != nil {
			log.Printf("plugin reject packet, err=%v", err)
			return status.Errorf(codes.Internal, "internal error, packet rejected, contact the administrator")
		}
		err = s.processClientPacket(pkt, c, conn, agStream)
		if err != nil {
			fmt.Printf("session=%v - failed processing client packet, err=%v", c.SessionID, err)
			return status.Errorf(codes.FailedPrecondition, "internal error, failed processing packet")
		}
	}
}

func (s *Server) processClientPacket(
	pkt *pb.Packet,
	client *client.Client,
	conn *connection.Connection,
	agentStream pb.Transport_ConnectServer) error {
	switch pb.PacketType(pkt.Type) {
	case pb.PacketGatewayConnectType:
		return s.processPacketGatewayConnect(pkt, client, conn, agentStream)
	default:
		_ = agentStream.Send(pkt)
	}
	return nil
}

func (s *Server) processPacketGatewayConnect(pkt *pb.Packet,
	client *client.Client,
	conn *connection.Connection,
	agentStream pb.Transport_ConnectServer) error {
	var clientArgs []string
	if pkt.Spec != nil {
		encArgs := pkt.Spec[pb.SpecClientExecArgsKey]
		if len(encArgs) > 0 {
			if err := pb.GobDecodeInto(encArgs, &clientArgs); err != nil {
				log.Printf("failed decoding args, err=%v", err)
			}
		}
	}
	var infoTypes []string
	for _, p := range getPlugins(client.SessionID) {
		if p.Plugin.Name() == pluginsdlp.Name {
			infoTypes = p.config
			break
		}
	}
	encConnectionParams, err := pb.GobEncode(&pb.AgentConnectionParams{
		EnvVars:      conn.Secret,
		CmdList:      conn.Command,
		ClientArgs:   clientArgs,
		DLPInfoTypes: infoTypes,
	})
	if err != nil {
		return fmt.Errorf("failed encoding connection params err=%v", err)
	}
	pktSpec := map[string][]byte{
		pb.SpecGatewaySessionID:         []byte(client.SessionID),
		pb.SpecConnectionType:           []byte(conn.Type),
		pb.SpecAgentConnectionParamsKey: encConnectionParams,
	}
	if s.GcpDLPRawCredentials != "" {
		pktSpec[pb.SpecAgentGCPRawCredentialsKey] = []byte(s.GcpDLPRawCredentials)
	}
	_ = agentStream.Send(&pb.Packet{
		Type: pb.PacketAgentConnectType.String(),
		Spec: pktSpec,
	})
	return nil
}

func (s *Server) disconnectClient(c *client.Client) {
	unbindClient(c.SessionID)
	c.Status = client.StatusDisconnected
	s.ClientService.Persist(c)
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

func (s *Server) clientGracefulShutdown(c *client.Client) {
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT)
	go func() {
		<-sigc
		s.disconnectClient(c)
		os.Exit(143)
	}()
}

func (s *Server) loadConnectPlugins(ctx *user.Context, config plugin.Config) ([]pluginConfig, error) {
	pluginsConfig := make([]pluginConfig, 0)
	for _, p := range allPlugins {
		p1, err := s.PluginService.FindOne(ctx, p.Name())
		if err != nil {
			log.Printf("failed retrieving plugin %q, err=%v", p.Name(), err)
			return nil, status.Errorf(codes.Internal, "failed registering plugins")
		}
		if p1 == nil {
			log.Printf("plugin not registered %q, skipping...", p.Name())
			continue
		}

		if p.Name() == pluginsaudit.Name {
			config.ParamsData[pluginsaudit.StorageWriterParam] = s.SessionService.Storage.NewGenericStorageWriter()
		}

		if p.Name() == pluginsreview.Name {
			config.ParamsData[pluginsreview.ServiceParam] = &s.ReviewService
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
					log.Printf("failed starting plugin %q, err=%v", p.Name(), err)
					return nil, status.Errorf(codes.Internal, "failed starting plugin")
				}

				if err = p.OnConnect(config); err != nil {
					log.Printf("plugin %q refused to accept connection %q, err=%v", p1.Name, config.SessionId, err)
					return nil, status.Errorf(codes.FailedPrecondition, err.Error())
				}

				pluginsConfig = append(pluginsConfig, ep)
				break
			}
		}
	}
	return pluginsConfig, nil
}

func (s *Server) pluginOnDisconnect(config plugin.Config) error {
	plugins := getPlugins(config.SessionId)
	for _, p := range plugins {
		return p.OnDisconnect(config)
	}
	return nil
}

func (s *Server) pluginOnReceive(config plugin.Config, pkt *pb.Packet) error {
	plugins := getPlugins(config.SessionId)
	for _, p := range plugins {
		if err := p.OnReceive(config, p.config, pkt); err != nil {
			log.Printf("session=%v - plugin %q rejected packet, err=%v",
				config.SessionId, p.Name(), err)
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
