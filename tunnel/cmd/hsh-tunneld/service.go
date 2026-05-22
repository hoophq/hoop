package main

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/hoophq/hoop/common/version"

	"github.com/hoophq/hoop/tunnel/addressing"
	"github.com/hoophq/hoop/tunnel/ipc"
	"github.com/hoophq/hoop/tunnel/portmap"
)

// daemonService implements ipc.Service against the running daemon's
// in-process state. It is the bridge between the HTTP control plane
// (which knows nothing about gVisor or gRPC) and the rest of the
// daemon (which knows nothing about HTTP).
//
// Endpoints whose implementation lives in later tickets (login, config
// writes, full reconnect) return ipc.ErrNotImplemented for now; the
// HTTP layer translates that to a 501 with code "not_implemented" so
// the UI can feature-detect cleanly.
type daemonService struct {
	// Static, set once at daemon startup.
	alloc         *addressing.Allocator
	subTypeByName map[string]string

	// Dynamic, protected by mu. lastError is updated by error paths in
	// the pipe handlers (future ticket); for now it is set only at
	// startup. running flips true once the netstack is up and accepting
	// flows.
	mu        sync.RWMutex
	since     time.Time
	running   bool
	lastError string

	// loggedIn reflects whether the daemon currently holds a usable
	// access token. In the current build (RD-215) the token is supplied
	// via env vars at startup and treated as immutable, so the value is
	// set once when the daemon initialises. RD-216 takes ownership of
	// the lifecycle (login, logout, refresh).
	loggedIn bool

	// apiURL is the configured hoop gateway HTTPS base URL. It is
	// surfaced via /v1/config so the UI can render where the daemon is
	// connecting; it is not yet mutable (RD-216 + RD-217).
	apiURL string

	// grpcURL, when non-empty, is the user-pinned gRPC address. Empty
	// means "auto-discovered from /api/serverinfo".
	grpcURL string
}

// newDaemonService constructs the Service implementation for the
// currently-running daemon. The caller must invoke MarkRunning after
// the netstack is fully wired so /v1/status reflects "running=true".
func newDaemonService(
	alloc *addressing.Allocator,
	subTypeByName map[string]string,
	apiURL, grpcURL string,
	loggedIn bool,
) *daemonService {
	return &daemonService{
		alloc:         alloc,
		subTypeByName: subTypeByName,
		apiURL:        apiURL,
		grpcURL:       grpcURL,
		loggedIn:      loggedIn,
	}
}

// MarkRunning is called by the daemon once the netstack is up and the
// TUN device is accepting flows. Idempotent.
func (s *daemonService) MarkRunning() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return
	}
	s.running = true
	s.since = time.Now()
}

// SetLastError lets the daemon's hot paths report transient errors
// (e.g. gRPC dial failure on a single TCP flow) for the UI to surface.
// Pass "" to clear after a successful recovery.
func (s *daemonService) SetLastError(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastError = msg
}

// ipc.Service implementation

func (s *daemonService) Status(context.Context) (ipc.StatusResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return ipc.StatusResponse{
		Running:       s.running,
		LoggedIn:      s.loggedIn,
		Since:         s.since,
		LastError:     s.lastError,
		DaemonVersion: version.Get().Version,
	}, nil
}

func (s *daemonService) Connections(context.Context) ([]ipc.Connection, error) {
	// The allocator owns the canonical name → IP map. We sort by name
	// so the response is stable across calls — Go map iteration order
	// is randomised and the UI lists this verbatim.
	names := s.alloc.Names()
	sort.Strings(names)
	out := make([]ipc.Connection, 0, len(names))
	for _, name := range names {
		ip, ok := s.alloc.LookupName(name)
		if !ok {
			continue
		}
		subType := s.subTypeByName[name]
		port, _ := portmap.CanonicalPort(subType)
		out = append(out, ipc.Connection{
			Name:         name,
			SubType:      subType,
			VirtualIP:    ip.String(),
			ExpectedPort: port,
		})
	}
	return out, nil
}

func (s *daemonService) LoginStart(context.Context) (ipc.LoginStartResponse, error) {
	// RD-216 owns this. Until then the endpoint is advertised so the UI
	// can feature-detect.
	return ipc.LoginStartResponse{}, ipc.ErrNotImplemented
}

func (s *daemonService) LoginPoll(context.Context, string) (ipc.LoginPollResponse, error) {
	return ipc.LoginPollResponse{}, ipc.ErrNotImplemented
}

func (s *daemonService) Logout(context.Context) error {
	return ipc.ErrNotImplemented
}

func (s *daemonService) Config(context.Context) (ipc.ConfigResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return ipc.ConfigResponse{
		APIURL:   s.apiURL,
		GrpcURL:  s.grpcURL,
		LogLevel: "info", // configurable in a later ticket
	}, nil
}

func (s *daemonService) UpdateConfig(context.Context, ipc.ConfigUpdateRequest) (ipc.ConfigResponse, error) {
	// Mutable config requires persistence (RD-216 lands /etc/hsh/config.toml).
	return ipc.ConfigResponse{}, ipc.ErrNotImplemented
}

func (s *daemonService) Reconnect(context.Context) error {
	// Full reconnect involves tearing down all active gRPC streams and
	// re-fetching the connection list. That logic ships with the
	// session-lifecycle ticket (RD-209); for now the endpoint exists so
	// the UI can detect it but returns 501.
	return ipc.ErrNotImplemented
}
