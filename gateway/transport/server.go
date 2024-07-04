package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/getsentry/sentry-go"
	commongrpc "github.com/runopsio/hoop/common/grpc"
	"github.com/runopsio/hoop/common/license"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/pgrest"
	"github.com/runopsio/hoop/gateway/review"
	"github.com/runopsio/hoop/gateway/security/idp"
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
		ReviewService review.Service

		IDProvider         *idp.Provider
		ApiHostname        string
		PyroscopeIngestURL string
		PyroscopeAuthToken string
		AgentSentryDSN     string
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
	orgCtx := pgrest.NewOrgContext(orgID)
	resp := connectionrequests.AgentPreConnect(orgCtx, agentID, req)
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
	if clientOrigin[0] == pb.ConnectionOriginAgent {
		return s.subscribeAgent(streamclient.NewAgent(gwctx.Agent, stream))
	}
	l, err := license.Parse(gwctx.UserContext.OrgLicenseData, s.ApiHostname)
	if err != nil {
		log.Warnf("license is not valid, verify error: %v", err)
		return status.Error(codes.FailedPrecondition, license.ErrNotValid.Error())
	}

	pluginCtx := &plugintypes.Context{
		Context: context.Background(),
		SID:     "",

		OrgID:          gwctx.UserContext.OrgID,
		OrgName:        gwctx.UserContext.OrgName, // TODO: it's not set when it's a service account
		OrgLicenseType: l.Payload.Type,
		UserID:         gwctx.UserContext.UserID,
		UserName:       gwctx.UserContext.UserName,
		UserEmail:      gwctx.UserContext.UserEmail,
		UserSlackID:    gwctx.UserContext.SlackID,
		UserGroups:     gwctx.UserContext.UserGroups,

		ConnectionID:      gwctx.Connection.ID,
		ConnectionName:    gwctx.Connection.Name,
		ConnectionType:    gwctx.Connection.Type,
		ConnectionSubType: gwctx.Connection.SubType,
		ConnectionCommand: gwctx.Connection.CmdEntrypoint,
		ConnectionSecret:  gwctx.Connection.Secrets,

		AgentID:   gwctx.Connection.AgentID,
		AgentName: gwctx.Connection.AgentName,
		AgentMode: gwctx.Connection.AgentMode,

		// added when initializing the streamclient proxy
		ClientVerb:   "",
		ClientOrigin: "",

		// TODO: deprecate it and allow the
		// audit plugin to update these attributes
		Script:   "",
		Labels:   nil,
		Metadata: nil,

		ParamsData: map[string]any{},
	}

	switch clientOrigin[0] {
	case pb.ConnectionOriginClientProxyManager:
		return s.proxyManager(streamclient.NewProxy(pluginCtx, stream))
	default:
		return s.subscribeClient(streamclient.NewProxy(pluginCtx, stream))
	}
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
