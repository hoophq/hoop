package httpproxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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
	proxyTokenHeader     = "Authorization"
	proxyTokenCookie     = "hoop_proxy_token"
	negativeAuthCacheTTL = 24 * time.Hour // Invalid auth keys will never be valid, so we will clean this up after hours
)

var instanceStore sync.Map

// negativeAuthEntry stores a cached authentication failure with timestamp
type negativeAuthEntry struct {
	err       error
	timestamp time.Time
}

// credentialError wraps errors related to invalid or expired credentials.
// Only these errors should be stored in the negative auth cache.
type credentialError struct {
	err error
}

func (e *credentialError) Error() string { return e.err.Error() }
func (e *credentialError) Unwrap() error { return e.err }

func newCredentialError(format string, a ...any) error {
	return &credentialError{err: fmt.Errorf(format, a...)}
}

// negativeAuthCache caches failed authentication attempts to avoid repeated database queries
var negativeAuthCache sync.Map // map[string]*negativeAuthEntry

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
	streamClient  pb.ClientTransport
	responseStore sync.Map    // stores response channels per connectionID
	closed        atomic.Bool // fast-fail flag to avoid mutex contention on session close
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

// RevokeBySecretKeyHash cancels the session for the given secret key hash, if one exists.
// This triggers the same cleanup flow as when a credential expires.
func (s *HttpProxyServer) RevokeBySecretKeyHash(secretKeyHash string) {
	if session, ok := s.sessionStore.Load(secretKeyHash); ok {
		if sess, ok := session.(*httpProxySession); ok {
			sess.cancelFn("credential revoked")
		}
	}
}

// ServeHTTP implements http.Handler
func (s *HttpProxyServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var proxyToken string

	// Check if token is in URL path: /<proxy-token> or /<proxy-token>/...
	// This is the initial browser request to set the cookie
	pathParts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
	if len(pathParts) > 0 && strings.HasPrefix(pathParts[0], "httpproxy") {
		// Token found in path (tokens start with "httpproxy")
		proxyToken = pathParts[0]

		// Detect if request is over HTTPS (directly or via reverse proxy)
		isSecure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"

		// Set cookie for future requests
		http.SetCookie(w, &http.Cookie{
			Name:     proxyTokenCookie,
			Value:    proxyToken,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Secure:   isSecure,
		})

		// Redirect to root (or remaining path) so browser uses the cookie
		// Use absolute URL for redirect to ensure it works correctly
		scheme := "http"
		if isSecure {
			scheme = "https"
		}
		redirectPath := "/"
		if len(pathParts) > 1 && pathParts[1] != "" {
			redirectPath = "/" + pathParts[1]
		}
		// Preserve query string if present
		if r.URL.RawQuery != "" {
			redirectPath += "?" + r.URL.RawQuery
		}
		// Use X-Forwarded-Host if behind reverse proxy, otherwise use Host
		host := r.Host
		if forwardedHost := r.Header.Get("X-Forwarded-Host"); forwardedHost != "" {
			host = forwardedHost
		}
		// Use absolute URL for redirect
		redirectURL := fmt.Sprintf("%s://%s%s", scheme, host, redirectPath)
		http.Redirect(w, r, redirectURL, http.StatusFound)
		return
	}
	// Check cookie (for subsequent browser requests)
	if proxyToken == "" {
		if cookie, err := r.Cookie(proxyTokenCookie); err == nil && cookie.Value != "" {
			proxyToken = cookie.Value
		}
	}
	//Check header (for curl/API usage)
	if proxyToken == "" {
		proxyToken = r.Header.Get(proxyTokenHeader)
	}
	if proxyToken == "" {
		proxyToken = r.Header.Get("X-Api-Key")
	}

	if proxyToken == "" {
		http.Error(w, "missing Authorization or X-Api-Key header", http.StatusUnauthorized)
		return
	}
	// token contains Bearer prefix
	// kubectl adds the tokeng with Bearer prefix
	if strings.HasPrefix(proxyToken, "Bearer ") {
		proxyToken = strings.TrimPrefix(proxyToken, "Bearer ")
	}

	secretKeyHash, err := keys.Hash256Key(proxyToken)
	if err != nil {
		log.Infof("failed hashing Authorization token proxy: %v", err)
		http.Error(w, "invalid proxy token", http.StatusUnauthorized)
		return
	}
	// check if the key is in the negative auth cache
	// avoid hammering the database with invalid requests
	// avoid goroutine for each request
	if cached, ok := negativeAuthCache.Load(secretKeyHash); ok {
		log.Debugf("negative auth cache hit for secret key hash: %s", secretKeyHash)
		entry := cached.(*negativeAuthEntry)
		// Check if cache entry is still valid (not expired)
		// we will clean this after 1 day just to clean the memory
		if time.Since(entry.timestamp) < negativeAuthCacheTTL {
			http.Error(w, entry.err.Error(), http.StatusUnauthorized)
			return
		}
		// Cache expired, remove it
		negativeAuthCache.Delete(secretKeyHash)
	}

	session, err := s.getOrCreateSession(secretKeyHash)
	if err != nil {
		var credErr *credentialError
		if errors.As(err, &credErr) {
			negativeAuthCache.Store(secretKeyHash, &negativeAuthEntry{
				err:       err,
				timestamp: time.Now(),
			})
		}
		log.Warnf("failed to get/create session: %v", err)
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
	dba, err := models.GetValidConnectionCredentialsBySecretKey(
		[]string{
			pb.ConnectionTypeHttpProxy.String(), pb.ConnectionTypeKubernetes.String(),
			pb.ConnectionTypeCommandLine.String(),
		},
		secretKeyHash)

	if err != nil {
		if err == models.ErrNotFound {
			return nil, newCredentialError("http proxy invalid proxy token credentials")
		}
		return nil, fmt.Errorf("http proxy failed obtaining credentials: %v", err)
	}

	if dba.ExpireAt.Before(time.Now().UTC()) {
		return nil, newCredentialError("http proxy token credentials expired")
	}

	return dba, nil
}

func (s *HttpProxyServer) getSessionOrRelease(secretKeyHash string) (*httpProxySession, error) {
	if session, ok := s.sessionStore.Load(secretKeyHash); ok {
		sess, ok := session.(*httpProxySession)
		if !ok {
			// Invalid session type, remove it
			s.sessionStore.Delete(secretKeyHash)
			return nil, fmt.Errorf("invalid session type for connection %s", secretKeyHash)
		}

		// Check if session context is still valid
		if sess.ctx.Err() != nil {
			log.Infof("http proxy session context error: %v, removing session for connection %s", sess.ctx.Err(), secretKeyHash)
			s.sessionStore.Delete(secretKeyHash)
			return nil, fmt.Errorf("http proxy session context error: %v", sess.ctx.Err())
		}
		log.Debugf("http proxy session found for connection %s", secretKeyHash)
		return sess, nil
	}
	// there is no session
	return nil, nil
}

func (s *HttpProxyServer) getOrCreateSession(secretKeyHash string) (*httpProxySession, error) {
	// First try to get existing valid session
	sess, err := s.getSessionOrRelease(secretKeyHash)
	if err != nil {
		return nil, err
	}
	if sess != nil {
		return sess, nil
	}

	// Validate credentials first
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

	sid := uuid.NewString()

	log.Infof("obtained http proxy access, id=%v, subject=%v, connection=%v, session_id=%v, expires-at=%v",
		dba.ID, dba.UserSubject, dba.ConnectionName, sid, dba.ExpireAt.Format(time.RFC3339))

	ctx, cancelFn := context.WithCancelCause(context.Background())
	ctx, timeoutCancelFn := context.WithTimeoutCause(ctx, ctxDuration,
		fmt.Errorf("http proxy connection access expired"))

	session := &httpProxySession{
		sid: sid,
		ctx: ctx,
		cancelFn: func(msg string, a ...any) {
			cancelFn(fmt.Errorf(msg, a...))
			timeoutCancelFn()
		},
	}

	transport.PollingUserToken(session.ctx, func(cause error) {
		session.cancelFn(cause.Error())
	}, tokenVerifier, dba.UserSubject)

	grpcOpts := []*grpc.ClientOptions{
		grpc.WithOption(grpc.OptionConnectionName, dba.ConnectionName),
		grpc.WithOption(grpckey.ImpersonateAuthKeyHeaderKey, grpckey.ImpersonateSecretKey),
		grpc.WithOption(grpckey.ImpersonateUserSubjectHeaderKey, dba.UserSubject),
		grpc.WithOption("origin", pb.ConnectionOriginClient),
		grpc.WithOption("verb", pb.ClientVerbConnect),
		grpc.WithOption("session-id", sid),
	}
	if dba.SessionID != "" {
		grpcOpts = append(grpcOpts, grpc.WithOption("credential-session-id", dba.SessionID))
	}

	// Do gRPC connection setup outside lock (this can take time)
	client, err := grpc.Connect(grpc.ClientConfig{
		ServerAddress: grpc.LocalhostAddr,
		Token:         "",
		UserAgent:     "httpproxy/grpc",
		Insecure:      !appconfig.Get().GatewayUseTLS(),
		TLSCA:         appconfig.Get().GrpcClientTLSCa(),
		TLSSkipVerify: true,
	}, grpcOpts...)
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
	if connectionType != pb.ConnectionTypeHttpProxy && connectionType != pb.ConnectionTypeKubernetes {
		session.cancelFn("unsupported connection type: %v", connectionType)
		return nil, fmt.Errorf("unsupported connection type: %v", connectionType)
	}

	log.With("sid", sid).Infof("http proxy session opened successfully")

	// Now start handling responses in background
	go session.handleAgentResponses(s, secretKeyHash)

	// close the gRPC stream to unblock any pending Send() calls
	// This prevents goroutine/resource leaks when many requests are in-flight during cancellation
	go func() {
		<-ctx.Done()
		log.Infof("http proxy session context done, sid=%s, cause=%v", sid, ctx.Err())
		// Set closed flag so pending Send goroutines can fast-fail without waiting for mutex
		session.closed.Store(true)
		log.Infof("http proxy session cleanup: closed flag set, sid=%s", sid)

		if session.streamClient != nil {
			err := session.streamClient.Send(&pb.Packet{
				Type:    pbclient.SessionClose,
				Payload: []byte("session expired"),
				Spec: map[string][]byte{
					pb.SpecGatewaySessionID: []byte(sid),
				},
			})
			if err != nil {
				log.Infof("http proxy session cleanup: failed sending session close, sid=%s, err=%v", sid, err)
			}
			_, _ = session.streamClient.Close()
			log.Infof("http proxy session cleanup: stream closed, sid=%s", sid)
		}
		// Cleanup will be done by handleAgentResponses defer, but we also try here as backup
		s.sessionStore.Delete(secretKeyHash)
		log.Infof("http proxy session cleanup: session removed from store, sid=%s", sid)
	}()

	// Store session; in case of reace condition, the first one wins and we close the new one
	s.mutex.Lock()
	if existingSession, ok := s.sessionStore.Load(secretKeyHash); ok {
		s.mutex.Unlock()
		// Another goroutine created the session first, close this one
		session.cancelFn("duplicate session, another session already exists")
		return existingSession.(*httpProxySession), nil
	}
	s.sessionStore.Store(secretKeyHash, session)
	s.mutex.Unlock()

	return session, nil
}

func (sess *httpProxySession) handleRequest(w http.ResponseWriter, r *http.Request) {
	// Early exit if session is already cancelled/closed to avoid spawning goroutines
	if sess.closed.Load() || sess.ctx.Err() != nil {
		http.Error(w, "session expired", http.StatusUnauthorized)
		return
	}

	// Generate sequential connection ID for response routing
	connectionID := strconv.FormatInt(sess.connCounter.Add(1), 10)
	log := log.With("sid", sess.sid, "conn", connectionID)
	log.Infof("handling request: %s %s", r.Method, r.URL.Path)

	// Create response channel for this request.
	// Buffer size of 100 provides headroom for SSE streaming where the agent sends
	// headers and each event chunk as separate gRPC packets.
	responseChan := make(chan []byte, 100)
	sess.responseStore.Store(connectionID, responseChan)
	defer sess.responseStore.Delete(connectionID)

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	// Determine proxy base URL from request (use Host header, not listenAddr)
	// This ensures redirects go to the correct host (e.g., dev.hoop.dev instead of 0.0.0.0)
	// Check X-Forwarded-Proto for requests behind reverse proxy
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	// Use X-Forwarded-Host if behind reverse proxy, otherwise use Host
	host := r.Host
	if forwardedHost := r.Header.Get("X-Forwarded-Host"); forwardedHost != "" {
		host = forwardedHost
	}
	proxyBaseURL := fmt.Sprintf("%s://%s", scheme, host)

	// Build raw HTTP request to forward
	rawRequest := fmt.Sprintf("%s %s %s\r\n", r.Method, r.URL.RequestURI(), r.Proto)
	for key, values := range r.Header {
		if key == proxyTokenHeader || key == "X-Api-Key" || key == "Host" {
			continue
		}

		for _, value := range values {
			rawRequest += fmt.Sprintf("%s: %s\r\n", key, value)
		}
	}
	rawRequest += "\r\n"
	rawRequest += string(body)

	// Send through gRPC with timeout context tied to the session
	ctx, cancel := context.WithTimeout(sess.ctx, 30*time.Second)
	defer cancel()

	// Send through gRPC
	// Use a channel to detect send completion
	sendErr := make(chan error, 1)
	go func() {
		// if session is closed, don't wait for mutex fail fast
		if sess.closed.Load() {
			sendErr <- fmt.Errorf("session closed")
			return
		}
		err := sess.streamClient.Send(&pb.Packet{
			Type:    pbagent.HttpProxyConnectionWrite,
			Payload: []byte(rawRequest),
			Spec: map[string][]byte{
				pb.SpecGatewaySessionID:   []byte(sess.sid),
				pb.SpecClientConnectionID: []byte(connectionID),
				pb.SpecHttpProxyBaseUrl:   []byte(proxyBaseURL),
			},
		})
		sendErr <- err
	}()

	// Set server header for identification purposes
	w.Header().Set("server", "httpproxy-hoopgateway")
	// Wait for send with timeout
	select {
	case err := <-sendErr:
		if err != nil {
			log.Errorf("failed sending request: %v", err)
			http.Error(w, "failed to forward request", http.StatusBadGateway)
			return
		}
		// when session is cancelled, fail fast
		if sess.ctx.Err() != nil {
			http.Error(w, "session expired", http.StatusUnauthorized)
			return
		}
	case <-sess.ctx.Done():
		http.Error(w, "session expired", http.StatusUnauthorized)
		return
	case <-ctx.Done():
		http.Error(w, "request send timeout", http.StatusGatewayTimeout)
		return
	}

	// Wait for response with shorter timeout
	httpTimeout := 60 * time.Second
	select {
	case <-sess.ctx.Done():
		http.Error(w, "session expired", http.StatusUnauthorized)
		return
	case <-time.After(httpTimeout):
		http.Error(w, "request timeout", http.StatusGatewayTimeout)
		return
	case response, ok := <-responseChan:
		if !ok {
			// Channel was closed (session canceled)
			http.Error(w, "session expired", http.StatusServiceUnavailable)
			return
		}
		// when session is cancelled, fail fast
		if sess.ctx.Err() != nil {
			http.Error(w, "session expired", http.StatusUnauthorized)
			return
		}
		// Parse and write response
		resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(response)), nil)
		if err != nil {
			log.Warnf("failed to parse response, writing raw: %v", err)
			if _, writeErr := w.Write(response); writeErr != nil {
				log.Errorf("failed writing raw response: %v", writeErr)
			}
			return
		}
		defer resp.Body.Close()

		// Check if this is a WebSocket upgrade response
		if resp.StatusCode == http.StatusSwitchingProtocols {
			sess.handleWebSocketUpgraded(w, response, responseChan, connectionID)
			return
		}

		// Check if this is an SSE streaming response.
		// SSE responses arrive as multiple gRPC packets (headers first, then chunks),
		// so we need to loop on responseChan to forward all chunks.
		if isSSEStreamingResponse(resp) {
			sess.handleSSEStream(w, resp, responseChan, connectionID)
			return
		}

		for key, values := range resp.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}
}

// isSSEStreamingResponse checks if the HTTP response is a Server-Sent Events stream.
// SSE responses require special handling because the agent sends headers and body
// chunks as separate gRPC packets, unlike regular responses which are sent atomically.
func isSSEStreamingResponse(resp *http.Response) bool {
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	return strings.HasPrefix(ct, "text/event-stream")
}

// handleSSEStream handles Server-Sent Events streaming responses.
//
// The agent sends SSE responses as multiple gRPC packets: the first contains
// HTTP headers only, and subsequent packets contain chunked body data (one per
// SSE event). This method loops on responseChan to forward all chunks to the
// HTTP client, flushing after each one so events are delivered in real-time.
//
// The stream ends when the responseChan is closed (agent finished), the session
// context is cancelled, or an idle timeout of 5 minutes is exceeded between
// consecutive chunks.
func (sess *httpProxySession) handleSSEStream(
	w http.ResponseWriter,
	resp *http.Response,
	responseChan chan []byte,
	connectionID string,
) {
	log := log.With("sid", sess.sid, "conn", connectionID, "type", "sse")
	log.Infof("SSE stream detected, starting streaming relay")

	// Copy response headers to the client
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)

	// Close the parsed response body — for SSE the first packet contains only
	// headers, so the body is empty. The actual SSE event data arrives as
	// subsequent gRPC packets via responseChan.
	if resp.Body != nil {
		resp.Body.Close()
	}

	// Flush initial headers to the client immediately
	flusher, ok := w.(http.Flusher)
	if ok {
		flusher.Flush()
	}

	// Stream subsequent chunks from the agent.
	// Each chunk is a raw HTTP chunked-encoding fragment produced by the agent's
	// httputil.NewChunkedWriter wrapping the gRPC streamWriter.
	const sseIdleTimeout = 90 * time.Second
	idleTimer := time.NewTimer(sseIdleTimeout)
	defer idleTimer.Stop()

	for {
		select {
		case <-sess.ctx.Done():
			log.Infof("SSE stream ended: session context done")
			return
		case <-idleTimer.C:
			log.Warnf("SSE stream ended: idle timeout (%v) exceeded", sseIdleTimeout)
			return
		case data, ok := <-responseChan:
			if !ok {
				// Channel closed — agent finished the SSE stream
				log.Infof("SSE stream ended: response channel closed")
				return
			}

			if _, err := w.Write(data); err != nil {
				log.Warnf("SSE stream ended: write error: %v", err)
				return
			}
			if flusher != nil {
				flusher.Flush()
			}

			// Detect chunked-encoding termination (zero-length chunk: "0\r\n").
			// The agent's httputil.ChunkedWriter.Close() writes this when the SSE
			// stream ends. The agent does NOT send TCPConnectionClose for SSE, so
			// this is how we know the stream is done.
			if bytes.Equal(bytes.TrimSpace(data), []byte("0")) {
				log.Infof("SSE stream ended: received chunked transfer terminator")
				return
			}

			// Reset idle timer after each successful chunk
			if !idleTimer.Stop() {
				select {
				case <-idleTimer.C:
				default:
				}
			}
			idleTimer.Reset(sseIdleTimeout)
		}
	}
}

func (sess *httpProxySession) handleWebSocketUpgraded(
	w http.ResponseWriter,
	upgradeResponse []byte,
	responseChan chan []byte,
	connectionID string,
) {
	log := log.With("sid", sess.sid, "conn", connectionID, "type", "websocket")

	// Hijack the connection to get raw TCP access
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		log.Errorf("ResponseWriter does not support hijacking")
		// adding internal server error because this could be some error from the upgrade on
		// the agent side
		http.Error(w, "WebSocket upgrade error", http.StatusInternalServerError)
		return
	}

	conn, bufrw, err := hijacker.Hijack()
	if err != nil {
		log.Errorf("hijack failed: %v", err)
		// error reading the tcp raw data
		http.Error(w, "WebSocket upgrade failed", http.StatusInternalServerError)
		return
	}
	defer conn.Close()

	// Write the upgrade response to client
	if _, err := conn.Write(upgradeResponse); err != nil {
		log.Errorf("failed to write upgrade response: %v", err)
		return
	}

	log.Infof("WebSocket connection upgraded, starting bidirectional pump")

	ctx, cancel := context.WithCancel(sess.ctx)
	defer cancel()

	// doing this because i am starting 2 goroutines
	// we need to be sure when both are done before returning and closing resources
	var wg sync.WaitGroup
	wg.Add(2)

	// Client → Agent (read from client, send via gRPC to the agent)
	go func() {
		defer wg.Done()
		defer cancel()

		// this number was more a guess based on typical websocket frame sizes
		// websocket frame size small < 16Kb, so 32Kb should be enough to cover most cases
		// network tcp typically uses 1.5KB packets, 32KB = 21 packets, good size batch
		// keep the memory small and efficient per connection
		//System page size	32KB is a multiple of common page sizes (4KB, 8KB), better memory alignment.
		//Kernel buffers	Default TCP socket buffers are often 64KB-128KB. 32KB reads don't oversaturate.
		// 16Kb might be to small -> 64Kb might be to large and wasteful for many connections
		buf := make([]byte, 32*1024)

		// First, flush any buffered data from hijack
		for bufrw.Reader.Buffered() > 0 {
			n, err := bufrw.Read(buf)
			if err != nil {
				break
			}
			if n > 0 {
				if err := sess.streamClient.Send(&pb.Packet{
					Type:    pbagent.HttpProxyConnectionWrite,
					Payload: buf[:n],
					Spec: map[string][]byte{
						pb.SpecGatewaySessionID:   []byte(sess.sid),
						pb.SpecClientConnectionID: []byte(connectionID),
					},
				}); err != nil {
					log.Errorf("failed to send buffered data: %v", err)
					return
				}
			}
		}

		// Read loop
		for {
			select {
			case <-ctx.Done():
				return
			default:
				log.Debugf("waiting for client data...")
			}

			// WebSocket spec recommends ping/pong every 30-60 seconds to detect dead connections
			conn.SetReadDeadline(time.Now().Add(60 * time.Second))
			n, err := conn.Read(buf)
			if err != nil {
				// Handle read errors (graceful exit on EOF)
				if err != io.EOF && ctx.Err() == nil {
					log.Infof("exiting quietly, client read error: %v", err)
				}
				return
			}
			if n > 0 {
				if sess.closed.Load() {
					log.Infof("session closed, stopping client read pump")
					return
				}
				if err := sess.streamClient.Send(&pb.Packet{
					Type:    pbagent.HttpProxyConnectionWrite,
					Payload: buf[:n],
					Spec: map[string][]byte{
						pb.SpecGatewaySessionID:   []byte(sess.sid),
						pb.SpecClientConnectionID: []byte(connectionID),
					},
				}); err != nil {
					log.Warnf("websocket failed to send data to agent: %v", err)
					return
				}
			}
		}
	}()

	// ReponseChan Agent → Client (read from responseChan, write to client)
	go func() {
		defer wg.Done()
		defer cancel()

		for {
			select {
			case <-ctx.Done():
				return
			case data, ok := <-responseChan:
				if !ok {
					log.Warnf("agent websocket response channel closed")
					return
				}
				//Pushing data to client (network should accept quickly)
				conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
				if _, err := conn.Write(data); err != nil {
					log.Errorf("failed to write to client: %v", err)
					return
				}
			}
		}
	}()

	wg.Wait()
	log.Infof("WebSocket connection closed")
}
func (sess *httpProxySession) handleAgentResponses(server *HttpProxyServer, secretKeyHash string) {
	defer func() {
		log.Infof("handleAgentResponses defer starting cleanup, sid=%s", sess.sid)

		// It will do proper stream cleanup (CloseSend)
		if sess.streamClient != nil {
			_, _ = sess.streamClient.Close()
		}

		//Close all pending response channels to unblock waiting goroutines
		// This prevents goroutine leaks when the session is canceled
		sess.responseStore.Range(func(key, value any) bool {
			if ch, ok := value.(chan []byte); ok {
				// Use select to avoid blocking if channel is already closed
				select {
				case <-ch:
					// Channel already closed or has data, don't close again
				default:
					close(ch) // Close channel to unblock waiting handleRequest goroutines
				}
			}
			return true
		})

		server.sessionStore.Delete(secretKeyHash)
		log.Warnf("handleAgentResponses: session removed from store, sid=%s", sess.sid)
	}()

	recvCh := grpc.NewStreamRecv(sess.ctx, sess.streamClient)
	for {
		var dstream *grpc.DataStream
		var ok bool // Declare ok here
		select {
		case <-sess.ctx.Done():
			// cleanUp will happen in defer
			return
		case dstream, ok = <-recvCh:
			if !ok {
				// Channel closed (EOF or stream closed)
				// This is normal when the agent disconnects
				log.Debugf("http proxy stream recv channel closed, sid=%s", sess.sid)
				return
			}
			if dstream == nil {
				// Should not happen, but handle gracefully
				continue
			}
		}

		pkt, err := dstream.Recv()
		if err != nil {
			// Handle EOF as a normal close, not an error
			if err == io.EOF {
				log.Debugf("http proxy stream EOF, sid=%s", sess.sid)
				return
			}
			sess.cancelFn("http-proxy received error from stream: %v", err)
			return
		}
		if pkt == nil {
			// Skip nil packets
			continue
		}

		switch pb.PacketType(pkt.Type) {
		case pbclient.HttpProxyConnectionWrite:
			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
			log.Debugf("received response packet, connectionID=%s, payload size=%d, sid=%s", connectionID, len(pkt.Payload), sess.sid)

			if ch, ok := sess.responseStore.Load(connectionID); ok {
				if responseChan, ok := ch.(chan []byte); ok {
					select {
					case responseChan <- pkt.Payload:
						log.Debugf("successfully routed response to connection %s, sid=%s", connectionID, sess.sid)
					case <-sess.ctx.Done():
						// Session canceled while trying to send
						return
					default:
						log.Warnf("response channel full for conn %s, sid=%s (packet dropped)", connectionID, sess.sid)
						// Don't delete the channel — the consumer (e.g. SSE streaming loop)
						// may catch up and resume reading. Deleting would permanently kill
						// the stream for this connection.
					}
				} else {
					log.Infof("invalid response channel type for conn %s, sid=%s", connectionID, sess.sid)
				}
			} else {
				log.Warnf("no response channel found for connectionID=%s, sid=%s (response dropped)", connectionID, sess.sid)
			}

		case pbclient.SessionClose:
			// Extract the error message from payload if present
			errMsg := "session closed by agent"
			if len(pkt.Payload) > 0 {
				errMsg = string(pkt.Payload)
			}
			log.Infof("session closed by agent, sid=%s, reason=%s", sess.sid, errMsg)
			sess.cancelFn(errMsg)
			return
		// this is the case when the agent closes the connection
		case pbclient.TCPConnectionClose:
			connectionID := string(pkt.Spec[pb.SpecClientConnectionID])
			log.Infof("connection closed by agent, connectionID=%s, sid=%s", connectionID, sess.sid)
			// Close only this connection's response channel
			if ch, ok := sess.responseStore.LoadAndDelete(connectionID); ok {
				if responseChan, ok := ch.(chan []byte); ok {
					close(responseChan)
				}
			}
			// Don't return - keep processing other connections!

		default:
			// Unknown packet type, log and ignore
			log.Debugf("unknown packet type received: %v, sid=%s", pkt.Type, sess.sid)
		}
	}
}
