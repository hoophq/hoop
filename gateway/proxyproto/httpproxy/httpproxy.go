package httpproxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"sync"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/grpc"
	"github.com/hoophq/hoop/common/keys"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/idp"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/proxyproto/grpckey"
	"github.com/hoophq/hoop/gateway/transport"
)

const (
	instanceKey      = "http_proxy_server"
	proxyTokenHeader = "Proxy-Token"
	proxyTokenCookie = "hoop_proxy_token"
)

var instanceStore sync.Map

type HttpProxyServer struct {
	sessionStore sync.Map // map[string]*httpProxySession
	httpServer   *http.Server
	listener     net.Listener
	listenAddr   string
	tlsConfig    *tls.Config
	mutex        sync.RWMutex
}

type httpProxySession struct {
	sid           string
	ctx           context.Context
	cancelFn      func(msg string, a ...any)
	proxyBaseURL  string
	streamClient  pb.ClientTransport
	responseStore sync.Map // stores response channels per connectionID
	connCounter   atomic.Int64
}

func GetServerInstance() *HttpProxyServer {
	instance, ok := instanceStore.Load(instanceKey)
	if ok {
		if server, ok := instance.(*HttpProxyServer); ok {
			return server
		}
	}
	server := &HttpProxyServer{}
	instanceStore.Store(instanceKey, server)
	return server
}

func (s *HttpProxyServer) Start(listenAddr string, tlsConfig *tls.Config) error {
	instance, ok := instanceStore.Load(instanceKey)
	if ok {
		if srv, ok := instance.(*HttpProxyServer); ok && srv.listener != nil {
			return nil
		}
	}

	lis, err := net.Listen("tcp4", listenAddr)
	if err != nil {
		return fmt.Errorf("failed listening to address %v, err=%v", listenAddr, err)
	}
	log.Infof("starting http proxy server at %v", listenAddr)

	server := &HttpProxyServer{
		listener:   lis,
		listenAddr: listenAddr,
		tlsConfig:  tlsConfig,
	}

	server.httpServer = &http.Server{
		Handler: server,
	}

	go func() {
		var err error
		if tlsConfig != nil {
			fmt.Printf("Starting HTTP Proxy Server with TLS\n")
			server.httpServer.TLSConfig = tlsConfig
			err = server.httpServer.ServeTLS(lis, "", "")
		} else {
			fmt.Printf("Starting HTTP Proxy Server without TLS\n")
			err = server.httpServer.Serve(lis)
		}
		if err != nil && err != http.ErrServerClosed {
			log.Errorf("http proxy server error: %v", err)
		}
	}()

	instanceStore.Store(instanceKey, server)
	return nil
}

func (s *HttpProxyServer) Stop() error {
	instance, loaded := instanceStore.LoadAndDelete(instanceKey)
	if !loaded {
		return nil
	}
	server, ok := instance.(*HttpProxyServer)
	if !ok {
		return nil
	}

	server.sessionStore.Range(func(key, value any) bool {
		if session, ok := value.(*httpProxySession); ok {
			session.cancelFn("proxy server is shutting down")
		}
		return true
	})

	if server.httpServer != nil {
		log.Infof("stopping http proxy server at %v", server.listenAddr)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.httpServer.Shutdown(ctx)
	}
	return nil
}

func (s *HttpProxyServer) ListenAddr() string { return s.listenAddr }

// ServeHTTP implements http.Handler
func (s *HttpProxyServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	var proxyToken string

	// Check if token is in URL path: /<proxy-token> or /<proxy-token>/...
	// This is the initial browser request to set the cookie
	pathParts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
	if len(pathParts) > 0 && strings.HasPrefix(pathParts[0], "http") {
		// Token found in path (tokens start with "http" prefix from generateSecretKey)
		proxyToken = pathParts[0]

		// Set cookie for future requests
		http.SetCookie(w, &http.Cookie{
			Name:     proxyTokenCookie,
			Value:    proxyToken,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			// Secure: true, // Enable if using HTTPS
		})

		// Redirect to root (or remaining path) so browser uses the cookie
		redirectPath := "/"
		if len(pathParts) > 1 && pathParts[1] != "" {
			redirectPath = "/" + pathParts[1]
		}
		http.Redirect(w, r, redirectPath, http.StatusFound)
		return
	}

	// Check cookie (for subsequent browser requests)
	if cookie, err := r.Cookie(proxyTokenCookie); err == nil && cookie.Value != "" {
		proxyToken = cookie.Value
	}

	//Check header (for curl/API usage)
	if proxyToken == "" {
		proxyToken = r.Header.Get(proxyTokenHeader)
	}
	if proxyToken == "" {
		http.Error(w, "missing Proxy-Token header", http.StatusUnauthorized)
		return
	}

	secretKeyHash, err := keys.Hash256Key(proxyToken)
	if err != nil {
		log.Errorf("failed hashing proxy token: %v", err)
		http.Error(w, "invalid proxy token", http.StatusUnauthorized)
		return
	}

	session, err := s.getOrCreateSession(secretKeyHash)
	if err != nil {
		log.Errorf("failed to get/create session: %v", err)
		// Clear invalid cookie
		http.SetCookie(w, &http.Cookie{
			Name:   proxyTokenCookie,
			Value:  "",
			Path:   "/",
			MaxAge: -1,
		})
		http.Error(w, "Invalid Cookie/Proxy-Token", http.StatusUnauthorized)
		return
	}

	session.handleRequest(w, r)
}

func (s *HttpProxyServer) getOrCreateSession(secretKeyHash string) (*httpProxySession, error) {
	if session, ok := s.sessionStore.Load(secretKeyHash); ok {
		return session.(*httpProxySession), nil
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	// Double-check after lock
	if session, ok := s.sessionStore.Load(secretKeyHash); ok {
		return session.(*httpProxySession), nil
	}

	dba, err := models.GetValidConnectionCredentialsBySecretKey(
		pb.ConnectionTypeHttpProxy.String(),
		secretKeyHash)

	if err != nil {
		if err == models.ErrNotFound {
			return nil, fmt.Errorf("invalid proxy token credentials")
		}
		return nil, fmt.Errorf("failed obtaining credentials: %v", err)
	}

	ctxDuration := dba.ExpireAt.Sub(time.Now().UTC())
	if dba.ExpireAt.Before(time.Now().UTC()) {
		return nil, fmt.Errorf("proxy token credentials expired")
	}

	tokenVerifier, _, err := idp.NewUserInfoTokenVerifierProvider()
	if err != nil {
		return nil, err
	}

	if err := transport.CheckUserToken(tokenVerifier, dba.UserSubject); err != nil {
		return nil, err
	}

	log.Infof("obtained http proxy access, id=%v, subject=%v, connection=%v, expires-at=%v",
		dba.ID, dba.UserSubject, dba.ConnectionName, dba.ExpireAt.Format(time.RFC3339))

	sid := uuid.NewString()
	ctx, cancelFn := context.WithCancelCause(context.Background())
	ctx, timeoutCancelFn := context.WithTimeoutCause(ctx, ctxDuration,
		fmt.Errorf("connection access expired"))

	scheme := "http"
	if s.tlsConfig != nil {
		scheme = "https"
	}
	session := &httpProxySession{
		sid:          sid,
		ctx:          ctx,
		proxyBaseURL: fmt.Sprintf("%s://%s", scheme, s.listenAddr),
		cancelFn: func(msg string, a ...any) {
			cancelFn(fmt.Errorf(msg, a...))
			timeoutCancelFn()
		},
	}

	transport.PollingUserToken(session.ctx, func(cause error) {
		session.cancelFn(cause.Error())
		s.sessionStore.Delete(secretKeyHash)
	}, tokenVerifier, dba.UserSubject)

	client, err := grpc.Connect(grpc.ClientConfig{
		ServerAddress: grpc.LocalhostAddr,
		Token:         "",
		UserAgent:     "httpproxy/grpc",
		Insecure:      !appconfig.Get().GatewayUseTLS(),
		TLSCA:         appconfig.Get().GrpcClientTLSCa(),
		TLSSkipVerify: true,
	},
		grpc.WithOption(grpc.OptionConnectionName, dba.ConnectionName),
		grpc.WithOption(grpckey.ImpersonateAuthKeyHeaderKey, grpckey.ImpersonateSecretKey),
		grpc.WithOption(grpckey.ImpersonateUserSubjectHeaderKey, dba.UserSubject),
		grpc.WithOption("origin", pb.ConnectionOriginClient),
		grpc.WithOption("verb", pb.ClientVerbConnect),
		grpc.WithOption("session-id", sid),
	)
	if err != nil {
		session.cancelFn("failed connecting to grpc server")
		return nil, fmt.Errorf("failed connecting to grpc server: %v", err)
	}
	session.streamClient = client

	// Send SessionOpen
	if err := client.Send(&pb.Packet{
		Type: pbagent.SessionOpen,
		Spec: map[string][]byte{pb.SpecGatewaySessionID: []byte(sid)},
	}); err != nil {
		session.cancelFn("failed sending open session")
		return nil, err
	}

	// Wait for SessionOpenOK before returning the session
	pkt, err := client.Recv()
	if err != nil {
		session.cancelFn("failed receiving session open response")
		return nil, fmt.Errorf("failed receiving session open response: %v", err)
	}

	if pb.PacketType(pkt.Type) != pbclient.SessionOpenOK {
		session.cancelFn("unexpected response type: %s", pkt.Type)
		return nil, fmt.Errorf("unexpected response: %s - %s", pkt.Type, string(pkt.Payload))
	}

	connectionType := pb.ConnectionType(pkt.Spec[pb.SpecConnectionType])
	if connectionType != pb.ConnectionTypeHttpProxy {
		session.cancelFn("unsupported connection type: %v", connectionType)
		return nil, fmt.Errorf("unsupported connection type: %v", connectionType)
	}

	log.With("sid", sid).Infof("http proxy session opened successfully")

	// Now start handling responses in background
	go session.handleAgentResponses(s, secretKeyHash)

	s.sessionStore.Store(secretKeyHash, session)

	go func() {
		<-ctx.Done()
		s.sessionStore.Delete(secretKeyHash)
	}()

	return session, nil
}

func (sess *httpProxySession) handleRequest(w http.ResponseWriter, r *http.Request) {
	connectionID := strconv.FormatInt(sess.connCounter.Add(1), 10)
	log := log.With("sid", sess.sid, "conn", connectionID)
	log.Infof("handling request: %s %s", r.Method, r.URL.Path)

	// Create response channel for this request
	responseChan := make(chan []byte, 1)
	sess.responseStore.Store(connectionID, responseChan)
	defer sess.responseStore.Delete(connectionID)

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	// Build raw HTTP request to forward
	rawRequest := fmt.Sprintf("%s %s %s\r\n", r.Method, r.URL.RequestURI(), r.Proto)
	for key, values := range r.Header {
		if key == proxyTokenHeader || key == "Host" {
			continue
		}

		for _, value := range values {
			rawRequest += fmt.Sprintf("%s: %s\r\n", key, value)
		}
	}
	rawRequest += "\r\n"
	rawRequest += string(body)

	// Send through gRPC
	err = sess.streamClient.Send(&pb.Packet{
		Type:    pbagent.HttpProxyConnectionWrite,
		Payload: []byte(rawRequest),
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID:   []byte(sess.sid),
			pb.SpecClientConnectionID: []byte(connectionID),
			pb.SpecHttpProxyBaseUrl:   []byte(sess.proxyBaseURL),
		},
	})
	if err != nil {
		log.Errorf("failed sending request: %v", err)
		http.Error(w, "failed to forward request", http.StatusBadGateway)
		return
	}
	// Wait for response
	select {
	case <-sess.ctx.Done():
		http.Error(w, "session expired", http.StatusGatewayTimeout)
	case <-time.After(5 * time.Minute):
		http.Error(w, "request timeout", http.StatusGatewayTimeout)
	case response := <-responseChan:
		// parse the  raw HTTP response
		resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(response)), nil)
		if err != nil {
			// If parsing fails, just write the raw response (fallback)
			log.Warnf("failed to parse response, writing raw: %v", err)
			w.Write(response)
			return
		}
		defer resp.Body.Close()

		// Copy headers from the proxied response
		for key, values := range resp.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}

		// Set status code
		w.WriteHeader(resp.StatusCode)

		// Copy body
		io.Copy(w, resp.Body)
	}
}

func (sess *httpProxySession) handleAgentResponses(server *HttpProxyServer, secretKeyHash string) {
	defer func() {
		_, _ = sess.streamClient.Close()
		server.sessionStore.Delete(secretKeyHash)
	}()

	for {
		pkt, err := sess.streamClient.Recv()
		if err != nil {
			sess.cancelFn("received error from stream: %v", err)
			return
		}
		if pkt == nil {
			sess.cancelFn("received nil packet")
			return
		}

		switch pb.PacketType(pkt.Type) {
		case pbclient.HttpProxyConnectionWrite:
			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
			if ch, ok := sess.responseStore.Load(connectionID); ok {
				if responseChan, ok := ch.(chan []byte); ok {
					select {
					case responseChan <- pkt.Payload:
					default:
						log.Warnf("response channel full for conn %s", connectionID)
					}
				}
			}

		case pbclient.TCPConnectionClose, pbclient.SessionClose:
			sess.cancelFn("session closed by server")
			return
		}
	}
}
