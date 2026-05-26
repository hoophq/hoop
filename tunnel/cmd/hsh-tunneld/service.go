package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/hoophq/hoop/common/version"

	"github.com/hoophq/hoop/tunnel/addressing"
	"github.com/hoophq/hoop/tunnel/daemonconfig"
	"github.com/hoophq/hoop/tunnel/ipc"
	"github.com/hoophq/hoop/tunnel/loginflow"
	"github.com/hoophq/hoop/tunnel/portmap"
)

// daemonService implements ipc.Service against the running daemon's
// in-process state. It is the bridge between the HTTP control plane
// (which knows nothing about gVisor or gRPC) and the rest of the
// daemon (which knows nothing about HTTP).
//
// State ownership:
//
//   - alloc + subTypeByName are immutable for the daemon's lifetime
//     after MarkRunning fires. They may be nil if the daemon started
//     without a token (fresh install): in that case Connections
//     returns an empty list rather than failing, so the UI can still
//     render a clean "no connections yet, please log in".
//
//   - cfg, configPath, and loggedIn are mutable and protected by mu.
//     The login / logout endpoints rewrite them and persist the
//     config file atomically before flipping loggedIn.
//
//   - Reconnect is still 501 — bringing the netstack up after a fresh
//     login requires the lazy-startup work in RD-209. For now, the
//     operator restarts the daemon after `hsh tunnel login`.
type daemonService struct {
	// Set once when the netstack is brought up. nil before login.
	alloc         *addressing.Allocator
	subTypeByName map[string]string

	// Login flow. Non-nil for the daemon's full lifetime; opts.APIURL
	// is updated when UpdateConfig sets a new api_url. (For now we
	// rebuild the flow on apiURL changes — cheap, no listener bind
	// happens until Start.)
	loginMu sync.Mutex
	login   *loginflow.Flow

	// Config persistence.
	configPath string

	mu        sync.RWMutex
	since     time.Time
	running   bool
	lastError string

	// loggedIn mirrors `len(cfg.Token) > 0`. We keep a separate field
	// rather than recomputing on every Status() call because Config
	// also needs to render with cfg's APIURL+GrpcURL, and we want a
	// single point of mutation under mu.
	loggedIn bool
	cfg      daemonconfig.Config

	// hooks lets main.go observe transitions (e.g. "token changed,
	// next user action should mention 'restart daemon'"). Optional;
	// nil hooks are skipped.
	onLoginSuccess func()
	onLogout       func()
}

// daemonServiceOptions configures newDaemonService.
type daemonServiceOptions struct {
	// ConfigPath is where the daemon persists its TOML config. Empty
	// means "do not persist" — only useful for in-process tests.
	ConfigPath string

	// InitialConfig is the config loaded from disk (or env) at
	// startup. Its Token field, when non-empty, makes the daemon
	// boot in the "logged in" state.
	InitialConfig daemonconfig.Config

	// OnLoginSuccess fires after a token has been persisted. Used by
	// main.go to log "restart the daemon to bring up the netstack"
	// when the daemon was already running without a token.
	OnLoginSuccess func()

	// OnLogout fires after a token has been cleared.
	OnLogout func()
}

func newDaemonService(opts daemonServiceOptions) (*daemonService, error) {
	s := &daemonService{
		configPath:     opts.ConfigPath,
		cfg:            opts.InitialConfig,
		loggedIn:       opts.InitialConfig.LoggedIn(),
		onLoginSuccess: opts.OnLoginSuccess,
		onLogout:       opts.OnLogout,
	}
	if opts.InitialConfig.APIURL != "" {
		flow, err := loginflow.New(loginflow.Options{
			APIURL:    opts.InitialConfig.APIURL,
			OnSuccess: s.persistTokenFromLogin,
		})
		if err != nil {
			return nil, fmt.Errorf("init login flow: %w", err)
		}
		s.login = flow
	}
	return s, nil
}

// AttachTunnel wires the allocator + subtype map into the service after
// the netstack is brought up. Called from run() once the tunnel is
// fully operational, alongside MarkRunning.
func (s *daemonService) AttachTunnel(alloc *addressing.Allocator, subTypeByName map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.alloc = alloc
	s.subTypeByName = subTypeByName
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

// persistTokenFromLogin is the OnSuccess callback handed to the login
// flow. It writes the new token to the daemon's TOML config under mu
// so a racing Config() / Status() never observes a half-applied state.
func (s *daemonService) persistTokenFromLogin(token string) error {
	s.mu.Lock()
	// Construct the new config first; only swap it in after the disk
	// write succeeds so a write failure leaves the in-memory state
	// untouched.
	next := s.cfg
	next.Token = token
	s.mu.Unlock()

	if s.configPath != "" {
		if err := daemonconfig.Save(s.configPath, next, daemonconfig.SaveOptions{}); err != nil {
			return fmt.Errorf("persist config: %w", err)
		}
	}

	s.mu.Lock()
	s.cfg = next
	s.loggedIn = true
	s.mu.Unlock()

	if s.onLoginSuccess != nil {
		s.onLoginSuccess()
	}
	return nil
}

// ipc.Service implementation -------------------------------------------

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
	s.mu.RLock()
	alloc := s.alloc
	subTypeByName := s.subTypeByName
	s.mu.RUnlock()

	if alloc == nil {
		// Fresh install (no token yet, or token loaded but the netstack
		// hasn't come up yet). Empty list is the right answer; the UI
		// distinguishes "no connections" from "not logged in" via
		// /v1/status's logged_in field.
		return []ipc.Connection{}, nil
	}

	names := alloc.Names()
	sort.Strings(names)
	out := make([]ipc.Connection, 0, len(names))
	for _, name := range names {
		ip, ok := alloc.LookupName(name)
		if !ok {
			continue
		}
		subType := subTypeByName[name]
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

func (s *daemonService) LoginStart(ctx context.Context) (ipc.LoginStartResponse, error) {
	s.loginMu.Lock()
	flow := s.login
	s.loginMu.Unlock()

	if flow == nil {
		return ipc.LoginStartResponse{}, errors.New(
			"login flow unavailable: set api_url first via PUT /v1/config (or HOOP_APIURL env var)")
	}

	url, state, err := flow.Start(ctx)
	if err != nil {
		if errors.Is(err, loginflow.ErrFlowInProgress) {
			return ipc.LoginStartResponse{}, fmt.Errorf(
				"a login attempt is already in progress; finish it in the browser or cancel before retrying")
		}
		return ipc.LoginStartResponse{}, err
	}
	return ipc.LoginStartResponse{
		BrowserURL: url,
		State:      state,
	}, nil
}

func (s *daemonService) LoginLocal(ctx context.Context, req ipc.LoginLocalRequest) error {
	s.mu.RLock()
	apiURL := s.cfg.APIURL
	s.mu.RUnlock()
	if apiURL == "" {
		return errors.New("local-auth login requires api_url; set it first via PUT /v1/config")
	}

	token, err := loginflow.LocalAuth(ctx, nil /* default http.Client */, apiURL, req.Email, req.Password)
	if err != nil {
		if errors.Is(err, loginflow.ErrInvalidLocalCredentials) {
			return errors.New("invalid email or password")
		}
		return err
	}

	if err := s.persistTokenFromLogin(token); err != nil {
		return fmt.Errorf("persist token: %w", err)
	}
	return nil
}

func (s *daemonService) LoginPoll(_ context.Context, state string) (ipc.LoginPollResponse, error) {
	s.loginMu.Lock()
	flow := s.login
	s.loginMu.Unlock()

	if flow == nil {
		return ipc.LoginPollResponse{}, errors.New("no login flow active")
	}
	result, ok := flow.Poll(state)
	if !ok {
		// Unknown state — either a typo or the daemon restarted
		// between Start and Poll. The UI should re-Start.
		return ipc.LoginPollResponse{Status: ipc.LoginPollStatus("error"), Error: "unknown state; restart login"}, nil
	}
	return ipc.LoginPollResponse{
		Status: ipc.LoginPollStatus(result.Status),
		Error:  result.Error,
	}, nil
}

func (s *daemonService) Logout(context.Context) error {
	s.mu.Lock()
	next := s.cfg
	next.Token = ""
	s.mu.Unlock()

	if s.configPath != "" {
		if err := daemonconfig.Save(s.configPath, next, daemonconfig.SaveOptions{}); err != nil {
			return fmt.Errorf("persist config: %w", err)
		}
	}

	s.mu.Lock()
	s.cfg = next
	s.loggedIn = false
	s.mu.Unlock()

	if s.onLogout != nil {
		s.onLogout()
	}
	return nil
}

func (s *daemonService) Config(context.Context) (ipc.ConfigResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	logLevel := s.cfg.LogLevel
	if logLevel == "" {
		logLevel = "info"
	}
	return ipc.ConfigResponse{
		APIURL:   s.cfg.APIURL,
		GrpcURL:  s.cfg.GrpcURL,
		LogLevel: logLevel,
	}, nil
}

func (s *daemonService) UpdateConfig(_ context.Context, req ipc.ConfigUpdateRequest) (ipc.ConfigResponse, error) {
	s.mu.Lock()
	next := s.cfg
	if req.APIURL != nil {
		next.APIURL = *req.APIURL
	}
	if req.GrpcURL != nil {
		next.GrpcURL = *req.GrpcURL
	}
	if req.LogLevel != nil {
		next.LogLevel = *req.LogLevel
	}
	s.mu.Unlock()

	if s.configPath != "" {
		if err := daemonconfig.Save(s.configPath, next, daemonconfig.SaveOptions{}); err != nil {
			return ipc.ConfigResponse{}, fmt.Errorf("persist config: %w", err)
		}
	}

	// Rebuild the login flow if the api_url changed (or first set).
	// We intentionally do this even if a previous flow is mid-attempt
	// — Start will refuse to launch a new one until the old one
	// resolves, which is the right user-visible behaviour.
	if req.APIURL != nil && next.APIURL != "" {
		newFlow, err := loginflow.New(loginflow.Options{
			APIURL:    next.APIURL,
			OnSuccess: s.persistTokenFromLogin,
		})
		if err != nil {
			return ipc.ConfigResponse{}, fmt.Errorf("rebuild login flow: %w", err)
		}
		s.loginMu.Lock()
		s.login = newFlow
		s.loginMu.Unlock()
	}

	s.mu.Lock()
	s.cfg = next
	logLevel := next.LogLevel
	if logLevel == "" {
		logLevel = "info"
	}
	s.mu.Unlock()

	return ipc.ConfigResponse{
		APIURL:   next.APIURL,
		GrpcURL:  next.GrpcURL,
		LogLevel: logLevel,
	}, nil
}

func (s *daemonService) Reconnect(context.Context) error {
	// Full reconnect involves tearing down all active gRPC streams and
	// re-fetching the connection list. That logic ships with RD-209.
	// For now the endpoint is advertised but returns 501 so the UI can
	// surface "restart the daemon to apply changes" instead of crashing.
	return ipc.ErrNotImplemented
}
