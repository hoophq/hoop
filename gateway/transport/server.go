package transport

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/getsentry/sentry-go"
	commongrpc "github.com/hoophq/hoop/common/grpc"
	"github.com/hoophq/hoop/common/license"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/pgrest"
	"github.com/hoophq/hoop/gateway/review"
	"github.com/hoophq/hoop/gateway/security/idp"
	"github.com/hoophq/hoop/gateway/transport/connectionrequests"
	"github.com/hoophq/hoop/gateway/transport/connectionstatus"
	authinterceptor "github.com/hoophq/hoop/gateway/transport/interceptors/auth"
	sessionuuidinterceptor "github.com/hoophq/hoop/gateway/transport/interceptors/sessionuuid"
	tracinginterceptor "github.com/hoophq/hoop/gateway/transport/interceptors/tracing"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	"github.com/hoophq/hoop/gateway/transport/streamclient"
	streamtypes "github.com/hoophq/hoop/gateway/transport/streamclient/types"
	"google.golang.org/grpc"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type (
	Server struct {
		pb.UnimplementedTransportServer

		TLSConfig     *tls.Config
		ReviewService review.Service
		IDProvider    *idp.Provider
		ApiHostname   string
	}
)

const listenAddr = "0.0.0.0:8010"

func (s *Server) StartRPCServer() {
	log.Printf("starting gateway at %v", listenAddr)
	listener, err := net.Listen("tcp", listenAddr)
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
	if s.TLSConfig != nil {
		grpcServer = grpc.NewServer(
			grpc.MaxRecvMsgSize(commongrpc.MaxRecvMsgSize),
			grpc.Creds(credentials.NewTLS(s.TLSConfig)),
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
	log.Infof("server transport created, tls=%v", s.TLSConfig != nil)
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

func GetAccessModesFromConnect(clientVerb string, clientOrigin string) string {
	switch {
	case clientVerb == pb.ClientVerbExec && clientOrigin == pb.ConnectionOriginClient:
		return "exec"
	case clientVerb == pb.ClientVerbExec && clientOrigin == pb.ConnectionOriginClientAPI:
		return "exec"
	case clientVerb == pb.ClientVerbConnect:
		return "connect"
	case clientVerb == pb.ClientVerbExec && clientOrigin == pb.ConnectionOriginClientAPIRunbooks:
		return "runbooks"
	default:
		return ""
	}
}

func (s *Server) Connect(stream pb.Transport_ConnectServer) (err error) {
	md, _ := metadata.FromIncomingContext(stream.Context())
	clientOrigin := md.Get("origin")
	clientVerb := md.Get("verb")

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

	// Verifying if the feature is enabled
	currentAccessMode := GetAccessModesFromConnect(clientVerb[0], clientOrigin[0])

	AccessModes := map[string]string{
		"exec":     gwctx.Connection.AccessModeExec,
		"connect":  gwctx.Connection.AccessModeConnect,
		"runbooks": gwctx.Connection.AccessModeRunbooks}

	if AccessModes[currentAccessMode] == "disabled" {
		return status.Error(codes.FailedPrecondition,
			fmt.Sprintf("the %v access mode connection has the %v feature disabled", gwctx.Connection.Name, currentAccessMode))
	}
	// End of verification of the feature enabled

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
