package transport

import (
	"bytes"
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
	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/agent"
	"github.com/runopsio/hoop/gateway/connection"
	"github.com/runopsio/hoop/gateway/notification"
	"github.com/runopsio/hoop/gateway/review"
	"github.com/runopsio/hoop/gateway/security/idp"
	"github.com/runopsio/hoop/gateway/session"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
	"github.com/runopsio/hoop/gateway/user"
	"google.golang.org/grpc"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type (
	Server struct {
		pb.UnimplementedTransportServer
		AgentService        agent.Service
		ConnectionService   connection.Service
		UserService         user.Service
		SessionService      session.Service
		ReviewService       review.Service
		NotificationService notification.Service

		IDProvider           *idp.Provider
		Profile              string
		GcpDLPRawCredentials string
		PluginRegistryURL    string
		PyroscopeIngestURL   string
		PyroscopeAuthToken   string
		AgentSentryDSN       string
		Analytics            user.Analytics

		RegisteredPlugins []plugintypes.Plugin

		StoreV2 *storagev2.Store
	}

	AnalyticsService interface {
		Track(ctx *types.APIContext, eventName string, properties map[string]any)
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
	var grpcServer *grpc.Server
	if tlsConfig != nil {
		grpcServer = grpc.NewServer(
			grpc.Creds(credentials.NewTLS(tlsConfig)),
			grpc.StreamInterceptor(s.AuthGrpcInterceptor),
		)
	}
	if grpcServer == nil {
		grpcServer = grpc.NewServer(grpc.StreamInterceptor(s.AuthGrpcInterceptor))
	}
	pb.RegisterTransportServer(grpcServer, s)
	s.handleGracefulShutdown()
	log.Infof("server transport created, tls=%v", tlsConfig != nil)
	if err := grpcServer.Serve(listener); err != nil {
		sentry.CaptureException(err)
		log.Fatalf("failed to serve: %v", err)
	}
}

func (s *Server) Connect(stream pb.Transport_ConnectServer) error {
	ctx := stream.Context()
	md, _ := metadata.FromIncomingContext(ctx)
	clientOrigin := md.Get("origin")
	if len(clientOrigin) == 0 {
		md.Delete("authorization")
		log.Debugf("client missing origin, client-metadata=%v", md)
		return status.Error(codes.InvalidArgument, "missing origin")
	}

	switch clientOrigin[0] {
	case pb.ConnectionOriginAgent:
		// keep compatibility with old clients
		// hoopagent/sdk or hoopagent/sidecar
		if strings.HasPrefix(mdget(md, "user-agent"), "hoopagent/s") {
			return s.subscribeAgentSidecar(stream)
		}
		return s.subscribeAgent(stream)
	case pb.ConnectionOriginClientProxyManager:
		return s.proxyManager(stream)
	default:
		return s.subscribeClient(stream)
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

func (s *Server) trackSessionStatus(sessionID, phase string, err error) {
	var errMsg *string
	if err != nil {
		v := err.Error()
		if len(v) > 150 {
			v = fmt.Sprintf("%v, [TRUNCATE %v bytes] ...", v[:150], len(v)-150)
		}
		errMsg = &v
	}
	// trackID will be unique for the same session id
	trackID, err := uuid.NewRandomFromReader(bytes.NewBufferString(sessionID))
	if err != nil {
		log.Errorf("failed generating track id from session %v, err=%v", sessionID, err)
		return
	}
	_, trackErr := s.SessionService.PersistStatus(&session.SessionStatus{
		ID:        trackID.String(),
		SessionID: sessionID,
		Phase:     phase,
		Error:     errMsg,
	})
	if trackErr != nil {
		log.Warnf("failed tracking session status, err=%v", trackErr)
	}
}

func (s *Server) validateSessionID(sessionID string) error {
	return s.SessionService.ValidateSessionID(sessionID)
}

func (s *Server) handleGracefulShutdown() {
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT)
	go func() {
		<-sigc
		ctx := s.disconnectAllClients()
		<-ctx.Done()
		if err := ctx.Err(); err != nil {
			if context.Canceled == err {
				log.Infof("gateway shutdown gracefully")
			} else {
				log.Errorf("gateway shutdown timeout (15s), force closing it, err=%v", err)
			}
		}
		os.Exit(143)
	}()
}

// disconnectAllClients closes the disconnect sink channel for all clients
func (s *Server) disconnectAllClients() context.Context {
	disconnectSink.mu.Lock()
	defer disconnectSink.mu.Unlock()

	var clientItems []string
	for key := range disconnectSink.items {
		clientItems = append(clientItems, key)
	}
	log.Infof("disconnecting all clients, length=%v, items=%v", len(disconnectSink.items), clientItems)
	ctx, cancelFn := context.WithTimeout(context.Background(), time.Second*25)
	go func() {

		defer cancelFn()
		for itemKey, disconnectCh := range disconnectSink.items {
			select {
			case disconnectCh <- fmt.Errorf("gateway shut down"):
			case <-time.After(time.Millisecond * 100):
				log.Errorf("timeout (100ms) send disconnect gateway error to sink")
			}
			// wait up to 0.5 seconds to close channel
			// continue to the next one if it takes more time
			select {
			case <-disconnectCh:
			case <-time.After(time.Millisecond * 500):
				log.Warnf("timeout (0.5s) on disconnecting channel %v, moving to next one", itemKey)
			}
		}
		// a state to signalize to shutdown xtdb when it's running
		// in a sidecar container.
		// https://kubernetes.io/docs/concepts/containers/container-lifecycle-hooks/#container-hooks
		preStopFilePath := "/tmp/xtdb-shutdown.placeholder"
		_ = os.Remove(preStopFilePath)
		_ = os.WriteFile(preStopFilePath, []byte(``), 0777)
	}()
	return ctx
}

// startDisconnectClientSink listen for disconnects when the disconnect channel is closed
// it timeout after 48 hours closing the client.
func (s *Server) startDisconnectClientSink(clientID, clientOrigin string, disconnectFn func(err error)) {
	disconnectSink.mu.Lock()
	defer disconnectSink.mu.Unlock()
	disconnectCh := make(chan error)
	disconnectSink.items[clientID] = disconnectCh
	log.With("id", clientID).Debugf("start disconnect sink for %v", clientOrigin)
	go func() {
		switch clientOrigin {
		case pb.ConnectionOriginAgent:
			err := <-disconnectCh
			// wait to get time to persist any resources performed async
			defer closeChWithSleep(disconnectCh, time.Millisecond*150)
			log.With("id", clientID).Infof("disconnecting agent client, reason=%v", err)
			disconnectFn(err)
		default:
			// wait to get time to persist any resources performed async
			defer closeChWithSleep(disconnectCh, time.Millisecond*150)
			select {
			case err := <-disconnectCh:
				log.With("id", clientID).Infof("disconnecting proxy client, reason=%v", err)
				disconnectFn(err)
			case <-time.After(time.Hour * 48):
				log.With("id", clientID).Warnf("timeout (48h), disconnecting proxy client")
				disconnectFn(fmt.Errorf("timeout (48h)"))
			}
		}
	}()
}

// disconnectClient closes the disconnect sink channel
// triggering the disconnect logic at startDisconnectClientSink
func (s *Server) disconnectClient(uid string, err error) {
	disconnectSink.mu.Lock()
	defer disconnectSink.mu.Unlock()
	disconnectCh, ok := disconnectSink.items[uid]
	if !ok {
		return
	}
	if err != nil {
		select {
		case disconnectCh <- err:
		case <-time.After(time.Millisecond * 100):
			log.With("uid", uid).Errorf("timeout (100ms) send disconnect error to sink")
		}
	}
	delete(disconnectSink.items, uid)
}

// closeChWithSleep sleep for d before closing the channel
func closeChWithSleep(ch chan error, d time.Duration) {
	time.Sleep(d)
	close(ch)
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
