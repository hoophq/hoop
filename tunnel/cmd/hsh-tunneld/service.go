package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/hoophq/hoop/common/version"

	"github.com/hoophq/hoop/tunnel/daemonconfig"
	"github.com/hoophq/hoop/tunnel/ipc"
	"github.com/hoophq/hoop/tunnel/loginflow"
	"github.com/hoophq/hoop/tunnel/portmap"
	"github.com/hoophq/hoop/tunnel/tunnelmgr"
)

// daemonService implements ipc.Service against the running daemon's
// in-process state. It is the bridge between the HTTP control plane
// (which knows nothing about gVisor or gRPC) and the rest of the
// daemon (which knows nothing about HTTP).
//
// State ownership:
//
//   - Manager owns the tunnel lifecycle (gateway dial, allocator,
//     netstack, routes). The service reads from it via Snapshot()
//     and asks it to BringUp / TearDown after login / logout.
//
//   - cfg, configPath, and loggedIn are mutable and protected by mu.
//     The login / logout endpoints rewrite them and persist the
//     config file atomically before flipping loggedIn.
//
//   - lastError is set by login/bring-up failure paths and surfaced
//     via /v1/status so the UI can render "tunnel won't come up
//     because gateway is unreachable" without grep-ing daemon logs.
type daemonService struct {
	mgr *tunnelmgr.Manager

	// parentCtx is the daemon's root context; passed through to
	// Manager.BringUp so any cancel from main propagates to the
	// per-tunnel goroutines.
	parentCtx context.Context

	// Login flow. Non-nil whenever cfg.APIURL is set; rebuilt by
	// UpdateConfig when api_url changes. Concurrency: loginMu
	// serialises the (read flow → call Start) pattern.
	loginMu sync.Mutex
	login   *loginflow.Flow

	// Config persistence.
	configPath string

	mu        sync.RWMutex
	lastError string
	loggedIn  bool
	cfg       daemonconfig.Config

	// hooks lets main.go observe transitions (e.g. "tunnel is up at
	// IP X, here's the resolvectl hint"). Optional.
	onTunnelUp   func(snap tunnelmgr.Snapshot)
	onTunnelDown func()
}

// daemonServiceOptions configures newDaemonService.
type daemonServiceOptions struct {
	// Manager owns the tunnel lifecycle. Required.
	Manager *tunnelmgr.Manager

	// ParentContext is the daemon's root context, propagated into
	// Manager.BringUp so cancellation works end-to-end.
	ParentContext context.Context

	// ConfigPath is where the daemon persists its TOML config. Empty
	// means "do not persist" — only useful for in-process tests.
	ConfigPath string

	// InitialConfig is the config loaded from disk (or env) at
	// startup. Its Token field, when non-empty, makes the daemon
	// boot in the "logged in" state.
	InitialConfig daemonconfig.Config

	// OnTunnelUp fires after Manager.BringUp succeeds — either at
	// startup or right after a login completes. main.go uses it to
	// print the resolvectl hint.
	OnTunnelUp func(tunnelmgr.Snapshot)

	// OnTunnelDown fires after Manager.TearDown completes — used
	// today only for the logout path.
	OnTunnelDown func()
}

func newDaemonService(opts daemonServiceOptions) (*daemonService, error) {
	if opts.Manager == nil {
		return nil, errors.New("daemonService: Manager is required")
	}
	if opts.ParentContext == nil {
		return nil, errors.New("daemonService: ParentContext is required")
	}
	s := &daemonService{
		mgr:          opts.Manager,
		parentCtx:    opts.ParentContext,
		configPath:   opts.ConfigPath,
		cfg:          opts.InitialConfig,
		loggedIn:     opts.InitialConfig.LoggedIn(),
		onTunnelUp:   opts.OnTunnelUp,
		onTunnelDown: opts.OnTunnelDown,
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

// SetLastError lets the daemon's hot paths report transient errors
// (e.g. gRPC dial failure on a single TCP flow) for the UI to surface.
// Pass "" to clear after a successful recovery.
func (s *daemonService) SetLastError(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastError = msg
}

// BringUpFromConfig is invoked by main.go at startup when the config
// has a token. It is a thin wrapper around Manager.BringUp that also
// updates the service's lastError on failure.
func (s *daemonService) BringUpFromConfig() error {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()
	if !cfg.LoggedIn() {
		return errors.New("daemonService: BringUpFromConfig: not logged in")
	}
	if err := s.mgr.BringUp(s.parentCtx, cfg); err != nil {
		s.SetLastError(err.Error())
		return err
	}
	s.SetLastError("")
	if s.onTunnelUp != nil {
		s.onTunnelUp(s.mgr.Snapshot())
	}
	return nil
}

// persistTokenFromLogin is the OnSuccess callback handed to the login
// flow. It writes the new token to the daemon's TOML config and then
// (RD-216 hot-reload) brings up the tunnel in-process — no daemon
// restart needed.
//
// Failure modes:
//   - Disk write failure → returned to the loginflow, surfaced in the
//     browser tab as "daemon failed to persist", flow ends in Error.
//   - Tunnel bring-up failure → token IS persisted (so next daemon
//     restart will retry) but lastError is set and the function
//     returns an error so the browser tab learns about it. This is
//     the right trade-off: a successful gateway login proves the
//     token works; an immediate bring-up failure is more likely a
//     transient network or permission issue than bad credentials.
func (s *daemonService) persistTokenFromLogin(token string) error {
	s.mu.Lock()
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

	// Hot-reload: try to bring the tunnel up with the new token.
	// If the tunnel was already up (rare — login flow refuses
	// concurrent attempts) we silently skip.
	if s.mgr.State() == tunnelmgr.StateIdle {
		if err := s.mgr.BringUp(s.parentCtx, next); err != nil {
			s.SetLastError("bring up after login: " + err.Error())
			return fmt.Errorf("bring up tunnel: %w", err)
		}
		s.SetLastError("")
		if s.onTunnelUp != nil {
			s.onTunnelUp(s.mgr.Snapshot())
		}
	}
	return nil
}

// ipc.Service implementation -------------------------------------------

func (s *daemonService) Status(context.Context) (ipc.StatusResponse, error) {
	s.mu.RLock()
	loggedIn := s.loggedIn
	lastErr := s.lastError
	s.mu.RUnlock()

	snap := s.mgr.Snapshot()
	return ipc.StatusResponse{
		Running:       snap.State == tunnelmgr.StateUp,
		LoggedIn:      loggedIn,
		Since:         snap.Since,
		LastError:     lastErr,
		DaemonVersion: version.Get().Version,
	}, nil
}

func (s *daemonService) Connections(context.Context) ([]ipc.Connection, error) {
	snap := s.mgr.Snapshot()
	if snap.Allocator == nil {
		return []ipc.Connection{}, nil
	}

	names := snap.Allocator.Names()
	sort.Strings(names)
	out := make([]ipc.Connection, 0, len(names))
	for _, name := range names {
		ip, ok := snap.Allocator.LookupName(name)
		if !ok {
			continue
		}
		subType := snap.SubTypeByName[name]
		port, _ := portmap.CanonicalPort(subType)
		conn := ipc.Connection{
			Name:         name,
			SubType:      subType,
			VirtualIP:    ip.String(),
			ExpectedPort: port,
		}
		if ipv4, ok := snap.Allocator.LookupNameV4(name); ok {
			conn.VirtualIPV4 = ipv4.String()
		}
		out = append(out, conn)
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

	// Hot tear-down: drop the live tunnel so the connection list
	// vanishes immediately and any in-flight gRPC pipes terminate.
	// We log and ignore TearDown errors — there is no useful recovery
	// path and we already cleared the token, so the user's intent
	// (be logged out) is preserved.
	if err := s.mgr.TearDown(); err != nil {
		s.SetLastError("tear down after logout: " + err.Error())
	} else {
		s.SetLastError("")
	}
	if s.onTunnelDown != nil {
		s.onTunnelDown()
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
	// True reconnect (drop in-flight streams, refetch connections,
	// rebind TUN) needs the lifecycle plumbing that lands with RD-209.
	// In the current build we have hot login + logout via TearDown /
	// BringUp; a forced reconnect would just be TearDown+BringUp here,
	// but exposing that on a fully-up tunnel is a different question
	// (active flows would be dropped) so we hold off until that ticket.
	return ipc.ErrNotImplemented
}

