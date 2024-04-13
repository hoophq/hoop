package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/getsentry/sentry-go"
	commongrpc "github.com/runopsio/hoop/common/grpc"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	apiconnections "github.com/runopsio/hoop/gateway/api/connections"
	"github.com/runopsio/hoop/gateway/notification"
	"github.com/runopsio/hoop/gateway/pgrest"
	pgagents "github.com/runopsio/hoop/gateway/pgrest/agents"
	"github.com/runopsio/hoop/gateway/review"
	"github.com/runopsio/hoop/gateway/security/idp"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"github.com/runopsio/hoop/gateway/transport/connectionrequests"
	"github.com/runopsio/hoop/gateway/transport/connectionstatus"
	authinterceptor "github.com/runopsio/hoop/gateway/transport/interceptors/auth"
	sessionuuidinterceptor "github.com/runopsio/hoop/gateway/transport/interceptors/sessionuuid"
	tracinginterceptor "github.com/runopsio/hoop/gateway/transport/interceptors/tracing"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
	"github.com/runopsio/hoop/gateway/transport/streamclient"
	streamtypes "github.com/runopsio/hoop/gateway/transport/streamclient/types"
	"google.golang.org/grpc"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type (
	Server struct {
		pb.UnimplementedTransportServer
		ReviewService       review.Service
		NotificationService notification.Service

		IDProvider           *idp.Provider
		GcpDLPRawCredentials string
		PluginRegistryURL    string
		PyroscopeIngestURL   string
		PyroscopeAuthToken   string
		AgentSentryDSN       string
	}
)

const listenAddr = "0.0.0.0:8010"

func loadServerCertificates() (*tls.Config, error) {
	tlsKeyEnc := os.Getenv("TLS_KEY")
	tlsCertEnc := os.Getenv("TLS_CERT")
	tlsCAEnc := os.Getenv("TLS_CA")
	if tlsKeyEnc == "" && tlsCertEnc == "" {
		return nil, nil
	}
	pemPrivateKeyData, err := base64.StdEncoding.DecodeString(tlsKeyEnc)
	if err != nil {
		return nil, fmt.Errorf("failed decoding TLS_KEY, err=%v", err)
	}
	pemCertData, err := base64.StdEncoding.DecodeString(tlsCertEnc)
	if err != nil {
		return nil, fmt.Errorf("failed decoding TLS_CERT, err=%v", err)
	}
	cert, err := tls.X509KeyPair(pemCertData, pemPrivateKeyData)
	if err != nil {
		return nil, fmt.Errorf("failed parsing key pair, err=%v", err)
	}
	var certPool *x509.CertPool
	if tlsCAEnc != "" {
		tlsCAData, err := base64.StdEncoding.DecodeString(tlsCAEnc)
		if err != nil {
			return nil, fmt.Errorf("failed decoding TLS_CA, err=%v", err)
		}
		certPool = x509.NewCertPool()
		if !certPool.AppendCertsFromPEM(tlsCAData) {
			return nil, fmt.Errorf("failed creating cert pool for TLS_CA")
		}
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      certPool,
	}, nil
}

func (s *Server) StartRPCServer() {
	log.Printf("starting gateway at %v", listenAddr)
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		sentry.CaptureException(err)
		log.Fatal(err)
	}

	if err := s.ValidateConfiguration(); err != nil {
		sentry.CaptureException(err)
		log.Fatal(err)
	}

	tlsConfig, err := loadServerCertificates()
	if err != nil {
		sentry.CaptureException(err)
		log.Fatal(err)
	}

	grpcInterceptors := grpc.ChainStreamInterceptor(
		sessionuuidinterceptor.New(),
		authinterceptor.New(s.IDProvider),
		tracinginterceptor.New(s.IDProvider.ApiURL),
	)
	var grpcServer *grpc.Server
	if tlsConfig != nil {
		grpcServer = grpc.NewServer(
			grpc.MaxRecvMsgSize(commongrpc.MaxRecvMsgSize),
			grpc.Creds(credentials.NewTLS(tlsConfig)),
			grpcInterceptors,
			authinterceptor.WithUnaryValidator(s.IDProvider),
		)
	}
	if grpcServer == nil {
		grpcServer = grpc.NewServer(
			grpc.MaxRecvMsgSize(commongrpc.MaxRecvMsgSize),
			grpcInterceptors,
			authinterceptor.WithUnaryValidator(s.IDProvider),
		)
	}
	pb.RegisterTransportServer(grpcServer, s)
	s.handleGracefulShutdown()
	log.Infof("server transport created, tls=%v", tlsConfig != nil)
	if err := grpcServer.Serve(listener); err != nil {
		sentry.CaptureException(err)
		log.Fatalf("failed to serve: %v", err)
	}
}

func (s *Server) PreConnect(ctx context.Context, req *pb.PreConnectRequest) (*pb.PreConnectResponse, error) {
	var gwctx authinterceptor.GatewayContext
	err := authinterceptor.ParseGatewayContextInto(ctx, &gwctx)
	orgID, agentID, agentName := gwctx.Agent.OrgID, gwctx.Agent.ID, gwctx.Agent.Name
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if orgID == "" || agentID == "" || agentName == "" {
		return nil, status.Errorf(codes.Internal, "missing agent context")
	}
	resp := connectionrequests.AgentPreConnect(orgID, agentID, req)
	if resp.Message != "" {
		err := fmt.Errorf("failed processing pre-connect, org=%v, agent=%v, reason=%v", orgID, agentName, err)
		log.Warn(err)
		sentry.CaptureException(err)
	}
	connectionstatus.SetOnlinePreConnect(pgrest.NewOrgContext(orgID), streamtypes.NewStreamID(agentID, req.Name))
	return resp, nil
}

func (s *Server) Connect(stream pb.Transport_ConnectServer) (err error) {
	md, _ := metadata.FromIncomingContext(stream.Context())
	clientOrigin := md.Get("origin")
	if len(clientOrigin) == 0 {
		md.Delete("authorization")
		log.Debugf("client missing origin, client-metadata=%v", md)
		return status.Error(codes.InvalidArgument, "missing origin")
	}

	var gwctx authinterceptor.GatewayContext
	err = authinterceptor.ParseGatewayContextInto(stream.Context(), &gwctx)
	if err != nil {
		log.Error(err)
		return err
	}

	switch clientOrigin[0] {
	case pb.ConnectionOriginAgent:
		return s.subscribeAgent(streamclient.NewAgent(gwctx.Agent, stream))
	case pb.ConnectionOriginClientProxyManager:
		// return s.proxyManager(stream)
		return fmt.Errorf("unavailable implementation")
	default:
		return s.subscribeClient(streamclient.NewProxy(&plugintypes.Context{
			Context: context.Background(),
			SID:     "",

			OrgID:       gwctx.UserContext.OrgID,
			OrgName:     gwctx.UserContext.OrgName, // TODO: it's not set when it's a service account
			UserID:      gwctx.UserContext.UserID,
			UserName:    gwctx.UserContext.UserName,
			UserEmail:   gwctx.UserContext.UserEmail,
			UserSlackID: gwctx.UserContext.SlackID,
			UserGroups:  gwctx.UserContext.UserGroups,

			ConnectionID:      gwctx.Connection.ID,
			ConnectionName:    gwctx.Connection.Name,
			ConnectionType:    gwctx.Connection.Type,
			ConnectionSubType: gwctx.Connection.SubType,
			ConnectionCommand: gwctx.Connection.CmdEntrypoint,
			ConnectionSecret:  gwctx.Connection.Secrets,

			AgentID:   gwctx.Connection.AgentID,
			AgentName: gwctx.Connection.AgentName,
			AgentMode: gwctx.Connection.AgentMode,

			ClientVerb:   "",
			ClientOrigin: "",

			// TODO: deprecate it and allow the
			// audit plugin to update these attributes
			Script:   "",
			Labels:   nil,
			Metadata: nil,

			ParamsData: map[string]any{},
		}, stream))
	}
}

func (s *Server) ValidateConfiguration() error {
	var js json.RawMessage
	if s.GcpDLPRawCredentials == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(s.GcpDLPRawCredentials), &js); err != nil {
		return fmt.Errorf("failed to parse env GOOGLE_APPLICATION_CREDENTIALS_JSON, it should be a valid JSON")
	}
	return nil
}

func (s *Server) handleGracefulShutdown() {
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT)
	go func() {
		signalNo := <-sigc
		timeout, cancelFn := context.WithTimeout(context.Background(), time.Second*10)
		defer cancelFn()
		select {
		case <-timeout.Done():
			log.Warn("timeout (10s) waiting for all proxies to disconnect")
		case <-streamclient.DisconnectAllProxies(fmt.Errorf("gateway shutdown")):
		}
		log.Warnf("gateway shutdown (%v)", signalNo)
		os.Exit(143)
	}()
}

func (s *Server) getConnection(name string, userCtx pgrest.Context) (*types.ConnectionInfo, error) {
	conn, err := apiconnections.FetchByName(userCtx, name)
	if err != nil {
		log.Errorf("failed retrieving connection %v, err=%v", name, err)
		sentry.CaptureException(err)
		return nil, status.Errorf(codes.Internal, "internal error, failed to obtain connection")
	}
	if conn == nil {
		return nil, nil
	}

	ag, err := pgagents.New().FetchOneByNameOrID(userCtx, conn.AgentId)
	if err != nil {
		log.Errorf("failed obtaining agent %v, err=%v", err)
		return nil, status.Errorf(codes.Internal, "internal error, failed to obtain agent from connection")
	}
	if ag == nil {
		return nil, status.Errorf(codes.NotFound, "agent not found")
	}
	return &types.ConnectionInfo{
		ID:            conn.ID,
		Name:          conn.Name,
		Type:          string(conn.Type),
		SubType:       conn.SubType,
		CmdEntrypoint: conn.Command,
		Secrets:       conn.Secrets,
		AgentID:       conn.AgentId,
		AgentMode:     ag.Mode,
		AgentName:     ag.Name,
	}, nil
}

func mdget(md metadata.MD, metaName string) string {
	data := md.Get(metaName)
	if len(data) == 0 {
		// keeps compatibility with old clients that
		// pass headers with underline. HTTP headers are not
		// accepted with underline for some servers, e.g.: nginx
		data = md.Get(strings.ReplaceAll(metaName, "-", "_"))
		if len(data) == 0 {
			return ""
		}
	}
	return data[0]
}
