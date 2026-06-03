package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

// Service is the contract the IPC server expects from the daemon. Every
// HTTP route corresponds to one (or two) methods here, and nothing
// else. Keeping the surface this narrow means:
//
//   - The HTTP layer is a pure adapter: it does no business logic and
//     no state management of its own.
//   - Tests can drive the server with a hand-rolled fake Service
//     implementation; no need to stand up a TUN device or a gateway.
//   - The daemon's main goroutine implements Service against the real
//     allocator / gateway / config and passes itself to NewServer.
//
// Methods that touch external state (Reconnect, login flow, config
// writes) must be safe to call concurrently with each other and with
// the read-only methods. Implementations should serialise internally if
// the underlying state isn't thread-safe.
type Service interface {
	// Status returns a snapshot of the daemon's current state. Must be
	// fast (the UI polls it at a few Hz) and must not block on network
	// I/O — return cached state instead.
	Status(ctx context.Context) (StatusResponse, error)

	// Connections returns the list of tunnelable connections the daemon
	// currently exposes under *.hoop. Empty list is a valid response
	// (e.g. the user is logged in but has access to nothing).
	Connections(ctx context.Context) ([]Connection, error)

	// LoginStart kicks off an OIDC login: allocates a state token,
	// binds the loopback callback listener, and returns the browser
	// URL plus state. The actual implementation lives in RD-216; the
	// stub used in this commit returns ErrNotImplemented.
	LoginStart(ctx context.Context) (LoginStartResponse, error)

	// LoginPoll reports the lifecycle status of the login attempt
	// identified by state. Returns ErrNotImplemented in the stub.
	LoginPoll(ctx context.Context, state string) (LoginPollResponse, error)

	// LoginLocal performs a synchronous email+password authentication
	// against the gateway's /api/localauth/login endpoint and persists
	// the returned token. It is the non-OIDC sibling of LoginStart,
	// used by self-hosted gateways whose `auth_method` is "local".
	//
	// Returns ErrNotImplemented if the daemon was built without
	// local-auth support (it isn't, today — both paths ship together).
	LoginLocal(ctx context.Context, req LoginLocalRequest) error

	// Logout clears the persisted access token and tears down any
	// active gateway gRPC streams. Returns ErrNotImplemented in the
	// stub.
	Logout(ctx context.Context) error

	// Config returns the current daemon-managed configuration, minus
	// the access token (which is never readable via the API).
	Config(ctx context.Context) (ConfigResponse, error)

	// UpdateConfig applies a partial config update. Nil fields are
	// left untouched. Returns the resulting config so the UI can
	// confirm the daemon's view matches its expectation.
	UpdateConfig(ctx context.Context, req ConfigUpdateRequest) (ConfigResponse, error)

	// Reconnect asks the daemon to tear down and re-establish its
	// gateway pipes asynchronously. Returns nil immediately (HTTP 202)
	// — completion is observable via Status.LastError + Running.
	Reconnect(ctx context.Context) error

	// Up brings the tunnel netstack up using the daemon's persisted
	// token, without touching authentication. It is the lifecycle
	// counterpart to Logout/Login: a logged-in daemon whose tunnel was
	// taken Down can be brought back Up without re-authenticating.
	//
	// Bring-up is synchronous: the response reflects the post-call
	// state. Calling Up on an already-Up tunnel is a no-op success
	// (AlreadyUp=true). Calling Up while logged out is a 409 conflict —
	// there is no token to dial the gateway with.
	Up(ctx context.Context) (TunnelUpResponse, error)

	// Down tears the tunnel netstack down while leaving the daemon's
	// token intact. Idempotent: calling Down on an already-idle daemon
	// succeeds with AlreadyDown=true. The user stays logged in and can
	// bring the tunnel back Up without re-authenticating.
	Down(ctx context.Context) (TunnelDownResponse, error)

	// RefreshConnections re-fetches the connection list from the gateway
	// and reconciles it into the live tunnel (new connections become
	// routable, deleted ones are hidden) without disturbing in-flight
	// flows. It is the on-demand counterpart to the daemon's periodic
	// auto-refresh. A no-op success when the tunnel is down. Returns the
	// post-refresh active connection count.
	RefreshConnections(ctx context.Context) (RefreshConnectionsResponse, error)
}

// ErrNotImplemented is the canonical sentinel a Service implementation
// returns from a method whose endpoint exists in the spec but is not
// yet wired up in the current daemon build. The HTTP layer translates
// it to a 501 response.
//
// This is the disciplined way to ship the contract early: every
// endpoint either works or returns 501 with code "not_implemented", so
// consumers (especially the TS UI in hoophq/hsh) can code against the
// full spec from day one and feature-detect by hitting the endpoint.
var ErrNotImplemented = errors.New("ipc: endpoint not implemented in this daemon build")

// ErrNotLoggedIn is the canonical sentinel a Service returns when an
// operation requires authentication that is not present — e.g. Up
// cannot bring the tunnel online without a token to dial the gateway.
// The HTTP layer translates it to a 409 Conflict (the daemon is in the
// wrong state for the request), distinguishing it from a 401 (the
// caller's control-token was rejected).
var ErrNotLoggedIn = errors.New("ipc: not logged in")

// ServerOptions configures NewServer.
type ServerOptions struct {
	// Service is the daemon-side implementation. Required.
	Service Service

	// TokenStore manages the bearer token rotation. Required. Callers
	// must call store.Rotate() before passing it in so the first
	// request can authenticate; the server itself never rotates.
	TokenStore TokenStore

	// Logger is used for request logs and lifecycle messages. Defaults
	// to log.Default(). Errors NEVER include the token; the auth
	// middleware logs success/failure but not the secret itself.
	Logger *log.Logger

	// ReadTimeout / WriteTimeout / IdleTimeout cap the HTTP request
	// lifetimes. Defaults are tuned for a local-only control plane
	// (short reads, mostly small responses, but the login-poll path
	// may be long-polled in a future version). Zero leaves the timeout
	// disabled.
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
}

// Server is the IPC HTTP server. Construct with NewServer and drive
// with Serve. Close it with Shutdown.
type Server struct {
	opts    ServerOptions
	handler http.Handler
	srv     *http.Server

	// closed flips to non-zero on first Shutdown call so concurrent
	// callers see the "already closing" state instead of double-closing
	// the underlying http.Server.
	closed atomic.Bool
}

// NewServer builds the IPC server. It does not bind any listener; the
// caller drives Serve(ln) with whatever net.Listener they want, which
// makes testing trivial (use net.Pipe-like listeners) and lets the
// daemon code own the platform-specific Listen call.
func NewServer(opts ServerOptions) (*Server, error) {
	if opts.Service == nil {
		return nil, errors.New("ipc: NewServer: Service is required")
	}
	if opts.TokenStore == nil {
		return nil, errors.New("ipc: NewServer: TokenStore is required")
	}
	if opts.Logger == nil {
		opts.Logger = log.Default()
	}
	if opts.ReadTimeout == 0 {
		opts.ReadTimeout = 15 * time.Second
	}
	if opts.WriteTimeout == 0 {
		opts.WriteTimeout = 30 * time.Second
	}
	if opts.IdleTimeout == 0 {
		opts.IdleTimeout = 60 * time.Second
	}

	s := &Server{opts: opts}
	s.handler = s.buildHandler()
	s.srv = &http.Server{
		Handler:      s.handler,
		ReadTimeout:  opts.ReadTimeout,
		WriteTimeout: opts.WriteTimeout,
		IdleTimeout:  opts.IdleTimeout,
		// ErrorLog routes connection-level errors (e.g. "client closed
		// connection during write") to our logger instead of stderr.
		ErrorLog: opts.Logger,
	}
	return s, nil
}

// Handler exposes the assembled http.Handler so tests can hit the
// server via httptest.NewServer without going through the unix-socket
// listener. Production code uses Serve.
func (s *Server) Handler() http.Handler { return s.handler }

// Serve accepts connections on ln and dispatches them to the IPC
// routes. It blocks until the listener is closed or Shutdown is called.
//
// Errors other than http.ErrServerClosed are returned to the caller so
// the daemon can decide whether to retry or exit. ErrServerClosed is
// the expected path during graceful shutdown and is squashed to nil.
func (s *Server) Serve(ln net.Listener) error {
	if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("ipc: serve: %w", err)
	}
	return nil
}

// Shutdown stops accepting new requests and waits for in-flight ones to
// finish, up to ctx's deadline. Safe to call multiple times: subsequent
// calls return nil without re-running the shutdown logic.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.closed.Swap(true) {
		return nil
	}
	return s.srv.Shutdown(ctx)
}

// buildHandler assembles the route mux and wraps it with auth +
// request logging. Returns the final http.Handler.
//
// Route registration is one-line-per-endpoint here so the spec is easy
// to audit at a glance against openapi.yaml.
func (s *Server) buildHandler() http.Handler {
	mux := http.NewServeMux()

	// v1 routes
	mux.HandleFunc("GET /v1/status", s.handleStatus)
	mux.HandleFunc("GET /v1/connections", s.handleConnections)
	mux.HandleFunc("POST /v1/login/start", s.handleLoginStart)
	mux.HandleFunc("GET /v1/login/poll", s.handleLoginPoll)
	mux.HandleFunc("POST /v1/login/local", s.handleLoginLocal)
	mux.HandleFunc("POST /v1/logout", s.handleLogout)
	mux.HandleFunc("GET /v1/config", s.handleConfigGet)
	mux.HandleFunc("PUT /v1/config", s.handleConfigPut)
	mux.HandleFunc("POST /v1/reconnect", s.handleReconnect)
	mux.HandleFunc("POST /v1/tunnel/up", s.handleTunnelUp)
	mux.HandleFunc("POST /v1/tunnel/down", s.handleTunnelDown)
	mux.HandleFunc("POST /v1/connections/refresh", s.handleConnectionsRefresh)

	// Any other path returns a JSON 404 instead of the default text body
	// so clients can parse the response uniformly.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, fmt.Sprintf("unknown route %s %s", r.Method, r.URL.Path), "not_found")
	})

	return authMiddleware(s.opts.TokenStore, requestLogger(s.opts.Logger, mux))
}

// requestLogger logs every request after it completes with method,
// path, status code, and duration. It never logs the Authorization
// header (or any other header content) — sensitive headers should never
// touch the log file.
func requestLogger(logger *log.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		// Wrap ResponseWriter so we can capture the status code without
		// trusting the handler to call WriteHeader.
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		logger.Printf("ipc %s %s %d %s", r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Microsecond))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.wroteHeader {
		return
	}
	r.status = code
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(code)
}

// ----------------------------------------------------------------------
// route handlers
// ----------------------------------------------------------------------

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	resp, err := s.opts.Service.Status(r.Context())
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleConnections(w http.ResponseWriter, r *http.Request) {
	conns, err := s.opts.Service.Connections(r.Context())
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if conns == nil {
		// Nil and empty are equivalent for the JSON consumer, but the
		// explicit empty slice marshals to `[]` instead of `null`.
		conns = []Connection{}
	}
	writeJSON(w, http.StatusOK, ConnectionsResponse{Connections: conns})
}

func (s *Server) handleLoginStart(w http.ResponseWriter, r *http.Request) {
	resp, err := s.opts.Service.LoginStart(r.Context())
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleLoginPoll(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	if state == "" {
		writeError(w, http.StatusBadRequest, "missing required query parameter `state`", "bad_request")
		return
	}
	resp, err := s.opts.Service.LoginPoll(r.Context(), state)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleLoginLocal(w http.ResponseWriter, r *http.Request) {
	var req LoginLocalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON body: %v", err), "bad_request")
		return
	}
	if req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "email and password are required", "bad_request")
		return
	}
	if err := s.opts.Service.LoginLocal(r.Context(), req); err != nil {
		s.writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.opts.Service.Logout(r.Context()); err != nil {
		s.writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleConfigGet(w http.ResponseWriter, r *http.Request) {
	resp, err := s.opts.Service.Config(r.Context())
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleConfigPut(w http.ResponseWriter, r *http.Request) {
	var req ConfigUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON body: %v", err), "bad_request")
		return
	}
	resp, err := s.opts.Service.UpdateConfig(r.Context(), req)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleReconnect(w http.ResponseWriter, r *http.Request) {
	if err := s.opts.Service.Reconnect(r.Context()); err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, ReconnectResponse{Accepted: true})
}

func (s *Server) handleTunnelUp(w http.ResponseWriter, r *http.Request) {
	resp, err := s.opts.Service.Up(r.Context())
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleTunnelDown(w http.ResponseWriter, r *http.Request) {
	resp, err := s.opts.Service.Down(r.Context())
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleConnectionsRefresh(w http.ResponseWriter, r *http.Request) {
	resp, err := s.opts.Service.RefreshConnections(r.Context())
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// writeServiceError translates a Service-layer error into the right
// HTTP status. The mapping is intentional and stable so the UI can
// distinguish "feature not yet built" from "transient failure" without
// string-matching error messages.
func (s *Server) writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotImplemented):
		writeError(w, http.StatusNotImplemented, err.Error(), "not_implemented")
	case errors.Is(err, ErrNotLoggedIn):
		writeError(w, http.StatusConflict, err.Error(), "not_logged_in")
	default:
		// Generic internal error. We DO include the underlying message
		// because this is a local-only control plane and the operator
		// running the UI is the same person debugging the daemon —
		// hiding details would just push them to journalctl.
		writeError(w, http.StatusInternalServerError, err.Error(), "internal")
	}
}

// writeJSON serialises v as JSON and writes it with the given status.
// On marshalling failure (extremely rare for the shapes we use) it
// degrades to a 500 with a synthesised error body so the client never
// sees an empty response.
func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		// At this point we've not written headers yet, so we can still
		// emit a clean error. We deliberately use http.Error rather
		// than recursing into writeError to avoid infinite loops on
		// pathological inputs.
		http.Error(w, `{"error":"failed to marshal response","code":"internal"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// writeError writes the canonical JSON error body documented in
// ErrorResponse, using status as the HTTP code.
func writeError(w http.ResponseWriter, status int, message, code string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	body, _ := json.Marshal(ErrorResponse{Error: message, Code: code})
	_, _ = w.Write(body)
}
