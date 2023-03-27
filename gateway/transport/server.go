package transport

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	"github.com/runopsio/hoop/gateway/agent"
	"github.com/runopsio/hoop/gateway/client"
	"github.com/runopsio/hoop/gateway/connection"
	"github.com/runopsio/hoop/gateway/notification"
	"github.com/runopsio/hoop/gateway/plugin"
	"github.com/runopsio/hoop/gateway/review"
	"github.com/runopsio/hoop/gateway/review/jit"
	"github.com/runopsio/hoop/gateway/security/idp"
	"github.com/runopsio/hoop/gateway/session"
	"github.com/runopsio/hoop/gateway/user"
	"google.golang.org/grpc"

	// "google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type (
	Server struct {
		pb.UnimplementedTransportServer
		AgentService         agent.Service
		ClientService        client.Service
		ConnectionService    connection.Service
		UserService          user.Service
		PluginService        plugin.Service
		SessionService       session.Service
		ReviewService        review.Service
		JitService           jit.Service
		NotificationService  notification.Service
		IDProvider           *idp.Provider
		Profile              string
		GcpDLPRawCredentials string
		PluginRegistryURL    string
		PyroscopeIngestURL   string
		PyroscopeAuthToken   string
		AgentSentryDSN       string
		Analytics            user.Analytics
	}

	AnalyticsService interface {
		Track(userID, eventName string, properties map[string]any)
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
		tlsCredentials := credentials.NewTLS(tlsConfig)
		grpcServer = grpc.NewServer(grpc.Creds(tlsCredentials))
	}
	if grpcServer == nil {
		grpcServer = grpc.NewServer()
	}
	pb.RegisterTransportServer(grpcServer, s)
	log.Infof("server transport created, tls=%v", tlsConfig != nil)
	if err := grpcServer.Serve(listener); err != nil {
		sentry.CaptureException(err)
		log.Fatalf("failed to serve: %v", err)
	}
}

func (s *Server) Connect(stream pb.Transport_ConnectServer) error {
	ctx := stream.Context()
	var token string
	md, _ := metadata.FromIncomingContext(ctx)
	o := md.Get("origin")
	if len(o) == 0 {
		md.Delete("authorization")
		log.Debugf("client missing origin, client-metadata=%v", md)
		return status.Error(codes.InvalidArgument, "missing origin")
	}

	origin := o[0]

	if s.Profile == pb.DevProfile {
		token = "x-hooper-test-token"
		if origin == pb.ConnectionOriginAgent {
			token = "x-agt-test-token"
		}
	} else {
		t := md.Get("authorization")
		if len(t) == 0 {
			log.Debugf("missing authorization header, client-metadata=%v", md)
			return status.Error(codes.Unauthenticated, "invalid authentication")
		}

		tokenValue := t[0]
		tokenParts := strings.Split(tokenValue, " ")
		if len(tokenParts) != 2 || tokenParts[0] != "Bearer" || tokenParts[1] == "" {
			log.Debugf("authorization header in wrong format, client-metadata=%v", md)
			return status.Error(codes.Unauthenticated, "invalid authentication")
		}

		token = tokenParts[1]
	}

	if origin == pb.ConnectionOriginAgent {
		return s.subscribeAgent(stream, token)
	}
	return s.subscribeClient(stream, token)
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
	_, trackErr := s.SessionService.PersistStatus(&session.SessionStatus{
		ID:        uuid.NewString(),
		SessionID: sessionID,
		Phase:     phase,
		Error:     errMsg,
	})
	if trackErr != nil {
		log.Printf("failed tracking session status, err=%v", trackErr)
	}
}

func (s *Server) validateSessionID(sessionID string) error {
	return s.SessionService.ValidateSessionID(sessionID)
}

// startDisconnectClientSink listen for disconnects when the disconnect channel is closed
// it timeout after 48 hours closing the client.
func (s *Server) startDisconnectClientSink(c *client.Client) {
	disconnectSink.mu.Lock()
	defer disconnectSink.mu.Unlock()
	disconnectCh := make(chan struct{})
	disconnectSink.items[c.SessionID] = disconnectCh
	disconnectFn := func() {
		unbindClient(c.SessionID)
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
	}
	log.With("session", c.SessionID).Debugf("start disconnect sink")
	go func() {
		select {
		case <-disconnectCh:
			log.With("session", c.SessionID).Infof("disconnecting client")
			disconnectFn()
		case <-time.After(time.Hour * 48):
			log.With("session", c.SessionID).Warnf("timeout (48h), disconnecting client")
			disconnectFn()
		}
	}()
}

// disconnectClient closes the disconnect sink channel
// triggering the disconnect logic at startDisconnectClientSink
func (s *Server) disconnectClient(sessionID string) {
	disconnectSink.mu.Lock()
	defer disconnectSink.mu.Unlock()
	disconnectCh, ok := disconnectSink.items[sessionID]
	if !ok {
		return
	}
	delete(disconnectSink.items, sessionID)
	close(disconnectCh)
}

func extractData(md metadata.MD, metaName string) string {
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
