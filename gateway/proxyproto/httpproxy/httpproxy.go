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
	instanceKey = "http_proxy_server"
	//TODO: chico the kubectl adds the token with Bearer prefix
	// and it uses Authorization header so using the same for consistency
	// we will be able to get kubernetes connections in our database
	// NOTE in the future we might want to support custom headers as well
	proxyTokenHeader = "Authorization"
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
	cleanUpOnce   sync.Once
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
		// when using freelens or kubernetes clients, when token expires
		// the client starts send a bunch of request this can exaust the server resources
		// With many concurrent requests holding connections open for up to 60 seconds,
		// this can exhaust file descriptors, goroutines, and memory, making the process unresponsive and affecting the REST API.
		ReadTimeout:       90 * time.Second,  // Maximum time to read the entire request
		WriteTimeout:      90 * time.Second,  // Maximum time to write the response
		IdleTimeout:       120 * time.Second, // Maximum time to wait for the next request when keep-alive is enabled
		ReadHeaderTimeout: 10 * time.Second,  // Maximum time to read request headers
		MaxHeaderBytes:    1 << 20,           // 1MB max header size
		ErrorLog:          log.NewStdHttpLogger(),
	}

	go func() {
		var err error
		if tlsConfig != nil {
			log.Infof("http proxy server using TLS")
			server.httpServer.TLSConfig = tlsConfig
			err = server.httpServer.ServeTLS(lis, "", "")
		} else {
			log.Infof("http proxy server using plain HTTP")
			err = server.httpServer.Serve(lis)
		}
		if err != nil && err != http.ErrServerClosed {
			log.Infof("http proxy server error: %v", err)
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
			session.cancelFn("http proxy server is shutting down")
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
		http.Error(w, "missing Authorization header", http.StatusUnauthorized)
		return
	}
	// token contains Bearer prefix
	// kubectl adds the tokeng with Bearer prefix
	if strings.HasPrefix(proxyToken, "Bearer ") {
		proxyToken = strings.TrimPrefix(proxyToken, "Bearer ")
	}

	secretKeyHash, err := keys.Hash256Key(proxyToken)
	if err != nil {
		log.Errorf("failed hashing Authorization token proxy: %v", err)
		http.Error(w, "invalid proxy token", http.StatusUnauthorized)
		return
	}

	session, err := s.getOrCreateSession(secretKeyHash)
	if err != nil {
		log.Warn("failed to get/create session: %v", err)
		// Clear invalid cookie
		http.SetCookie(w, &http.Cookie{
			Name:   proxyTokenCookie,
			Value:  "",
			Path:   "/",
			MaxAge: -1,
		})
		http.Error(w, "Invalid Cookie/Authorization", http.StatusUnauthorized)
		return
	}

	session.handleRequest(w, r)
}

func getValidConnectionCredentials(secretKeyHash string) (*models.ConnectionCredentials, error) {
	// Check cache first
	dba, err := models.GetValidConnectionCredentialsBySecretKey(
		pb.ConnectionTypeHttpProxy.String(),
		secretKeyHash)

	if err != nil {
		if err == models.ErrNotFound {
			return nil, fmt.Errorf("http proxy invalid proxy token credentials")
		}
		return nil, fmt.Errorf("http proxy failed obtaining credentials: %v", err)
	}

	if dba.ExpireAt.Before(time.Now().UTC()) {
		return nil, fmt.Errorf("http proxy token credentials expired")
	}

	return dba, nil
}

func (s *HttpProxyServer) getSessionOrRelease(secretKeyHash string) (*httpProxySession, error) {

	if session, ok := s.sessionStore.Load(secretKeyHash); ok {
		sess := session.(*httpProxySession)
		// Check if session context is still valid
		if sess.ctx.Err() != nil {
			log.Infof("http proxy session context error: %v, removing session for connection %s", sess.ctx.Err(), secretKeyHash)
			// Session context has error, remove it and will create a new one below

			sess.cleanUpOnce.Do(func() {
				s.sessionStore.Delete(secretKeyHash)
			})
			return nil, fmt.Errorf("http proxy credentials invalid/expired, for credentials")
		}
		log.Infof("http proxy session found for connection %s", secretKeyHash)
		return sess, nil
	}
	// there is no session
	return nil, nil
}

func (s *HttpProxyServer) getOrCreateSession(secretKeyHash string) (*httpProxySession, error) {
	// First try to get existing session without lock
	sess, err := s.getSessionOrRelease(secretKeyHash)
	if err != nil {
		return nil, err
	}
	if sess != nil {
		return sess, nil
	}

	// Credentials were already validated above, but validate again after lock
	// to ensure they're still valid (in case they expired between checks)
	dba, err := getValidConnectionCredentials(secretKeyHash)
	if err != nil {
		return nil, err
	}
	ctxDuration := time.Until(dba.ExpireAt)

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
		fmt.Errorf("http proxy connection access expired"))

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
		cleanUpOnce: sync.Once{},
	}

	go func() {
		<-ctx.Done()
		log.Infof("http proxy session context done, sid=%s, cause=%v", sid, ctx.Err())
		session.cleanUpOnce.Do(func() {
			s.sessionStore.Delete(secretKeyHash)
		})
	}()

	transport.PollingUserToken(session.ctx, func(cause error) {
		session.cancelFn(cause.Error())
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

	return session, nil
}

func (sess *httpProxySession) handleRequest(w http.ResponseWriter, r *http.Request) {

	connectionID := strconv.FormatInt(sess.connCounter.Add(1), 10)
	log := log.With("sid", sess.sid, "conn", connectionID)
	log.Infof("handling request: %s %s", r.Method, r.URL.Path)

	// Create response channel for this request
	// TODO: chico increase buffer size to prevent blocking.
	responseChan := make(chan []byte, 10) // changed for buffering
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

	// Send through gRPC with timeout context
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Send through gRPC
	// Use a channel to detect send completion
	sendErr := make(chan error, 1)
	go func() {
		err := sess.streamClient.Send(&pb.Packet{
			Type:    pbagent.HttpProxyConnectionWrite,
			Payload: []byte(rawRequest),
			Spec: map[string][]byte{
				pb.SpecGatewaySessionID:   []byte(sess.sid),
				pb.SpecClientConnectionID: []byte(connectionID),
				pb.SpecHttpProxyBaseUrl:   []byte(sess.proxyBaseURL),
			},
		})
		sendErr <- err
	}()

	w.Header().Set("server", "httpproxy-hoopgateway")
	// Wait for send with timeout
	select {
	case err := <-sendErr:
		if err != nil {
			log.Errorf("failed sending request: %v", err)
			http.Error(w, "failed to forward request", http.StatusBadGateway)
			return
		}
		if sess.ctx.Err() != nil {
			http.Error(w, "session expired", http.StatusUnauthorized)
			return
		}

	case <-ctx.Done():
		http.Error(w, "request send timeout", http.StatusGatewayTimeout)
		return
	}

	// Wait for response with shorter timeout
	httpTimeout := 60 * time.Second
	select {
	case <-time.After(httpTimeout):
		http.Error(w, "request timeout", http.StatusGatewayTimeout)
	case response := <-responseChan:
		if sess.ctx.Err() != nil {
			http.Error(w, "session expired", http.StatusUnauthorized)
			return
		}
		// Parse and write response
		resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(response)), nil)
		if err != nil {
			log.Warnf("failed to parse response, writing raw: %v", err)
			w.Write(response)
			return
		}
		defer resp.Body.Close()

		for key, values := range resp.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}
}

func (sess *httpProxySession) handleAgentResponses(server *HttpProxyServer, secretKeyHash string) {
	defer func() {
		_, _ = sess.streamClient.Close()
		sess.cleanUpOnce.Do(func() {
			server.sessionStore.Delete(secretKeyHash)
		})
	}()

	recvCh := grpc.NewStreamRecv(sess.ctx, sess.streamClient)
	for {
		var dstream *grpc.DataStream
		select {
		case <-sess.ctx.Done():

			// cleanUp will happen in defer
			return
		case dstream = <-recvCh:
			if dstream == nil {
				// Channel closed
				return
			}
		}

		pkt, err := dstream.Recv()
		if err != nil {
			sess.cancelFn("http-proxy received error from stream: %v", err)
			return
		}
		if pkt == nil {
			sess.cancelFn("http-proxy received nil packet")
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
						log.Infof("response channel full for conn %s", connectionID)
					}
				}
			}

		case pbclient.TCPConnectionClose, pbclient.SessionClose:
			sess.cancelFn("session closed by server")
			return

		default:
			// Unknown packet type, log and ignore
			log.Infof("unknown packet type received: %v", pkt.Type)
		}
	}
}
