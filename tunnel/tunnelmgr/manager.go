// Package tunnelmgr owns the lifecycle of the per-session tunnel:
// gateway dial, connection fetch, IP allocation, gVisor netstack, and
// the platform routes that point the host at the daemon's TUN device.
//
// The daemon's main process holds exactly one Manager and drives it
// through three actions:
//
//   - BringUp(cfg): build a fresh tunnel from a logged-in config.
//     Fails fast if the daemon is already up or the credentials don't
//     work.
//   - TearDown(): close the netstack, drop the routes, release all
//     resources. Safe to call when idle (returns nil).
//   - Snapshot(): read-only view consumed by /v1/status and
//     /v1/connections. Cheap and lock-free for callers.
//
// The contract guarantees:
//
//   - At most one tunnel is ever active. Concurrent BringUp / TearDown
//     calls serialise on an internal mutex; intermediate readers see
//     the previous-tunnel state until the transition commits.
//   - BringUp + TearDown are atomic with respect to readers: a reader
//     that calls Snapshot() while a transition is in flight observes
//     either the pre-state or the post-state, never half-applied
//     state.
//   - Parent context cancellation tears the tunnel down. The daemon
//     binds the root context to SIGTERM, so Ctrl-C / `systemctl stop`
//     always release the TUN device cleanly.
//
// The package is deliberately platform-neutral above the netstack
// abstraction; the route configuration on Linux/macOS is the same
// code path tested as part of RD-176. Windows TUN support is gated by
// the same build tags as the netstack package.
package tunnelmgr

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/netip"
	"sync"
	"time"

	"github.com/hoophq/hoop/tunnel/addressing"
	"github.com/hoophq/hoop/tunnel/client"
	"github.com/hoophq/hoop/tunnel/daemonconfig"
	"github.com/hoophq/hoop/tunnel/netstack"
	"github.com/hoophq/hoop/tunnel/resolved"
	"github.com/hoophq/hoop/tunnel/resolver"
)

// State enumerates the manager lifecycle.
//
// Idle  → no netstack, no routes, no goroutines. The daemon is "logged
//         out" or simply hasn't been asked to bring the tunnel up yet.
// Up    → netstack is serving TCP + DNS, routes are configured, the
//         gateway gRPC is dialable from the TUN side.
//
// We intentionally do NOT expose the transient BringingUp / TearingDown
// values — callers should observe steady states only. Internally we
// gate transitions with a mutex so there's no need for a third state
// in the public API.
type State int

const (
	StateIdle State = iota
	StateUp
)

func (s State) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StateUp:
		return "up"
	default:
		return "unknown"
	}
}

// Snapshot is the read-only view the IPC layer uses to answer
// /v1/status and /v1/connections without having to know anything about
// gVisor or the route table. All fields are copy-by-value or
// immutable-by-convention pointers; callers may keep the Snapshot
// past Manager mutations.
type Snapshot struct {
	State State

	// Since is the wall-clock instant BringUp completed for the
	// current tunnel, or the zero time when idle. The IPC layer
	// surfaces this on /v1/status.
	Since time.Time

	// Allocator is the address allocator for the current session, or
	// nil when idle. Retained so callers can resolve a name→IP for the
	// active connections in Connections below.
	Allocator *addressing.Allocator

	// Connections is the set of currently-active tunnelable connections
	// (name + subtype), captured under the registry lock at Snapshot
	// time so callers can range over it without racing a concurrent
	// refresh. nil/empty when idle. Connections deleted on the gateway
	// are excluded even though their IP stays reserved in Allocator.
	Connections []ConnInfo

	// HostAddr / Gateway are the addresses the kernel and gVisor own
	// on the current session's /48. Zero when idle.
	HostAddr netip.Addr
	Gateway  netip.Addr

	// DeviceName is the resolved TUN device name (e.g. "tun0").
	// Empty when idle.
	DeviceName string

	// ResolvedConfigured is true when the daemon successfully
	// registered the TUN interface with systemd-resolved during
	// bring-up. False when the host doesn't run resolved (the
	// banner falls back to the manual-hint block) or when the
	// registration failed (logged separately). Reading this is
	// the canonical way to ask "does the host resolve *.hoop
	// natively right now".
	ResolvedConfigured bool
}

// Logger is the minimal surface the manager needs from a logger.
// Matches *log.Logger and keeps the test suite free of testify.
type Logger interface {
	Printf(format string, v ...any)
}

// Options configures a Manager. Most fields are required; defaults
// are documented per field below.
type Options struct {
	// Logger receives lifecycle messages and per-flow errors. Required.
	Logger Logger

	// SessionSeed feeds the allocator's prefix derivation. Different
	// seeds yield different /48s; the same seed yields a deterministic
	// IP map so users get the same `<conn>.hoop` IP across daemon
	// restarts.
	SessionSeed string

	// TLD is the apex domain the daemon owns ("hoop" by default).
	// Connection names live at "<conn>.<TLD>".
	TLD string

	// DeviceName, when non-empty, pins the TUN device name. Empty
	// lets the kernel pick (tun0, tun1, ...).
	DeviceName string

	// HSHTLSSkipVerify and HSHTLSServerName plumb through to the
	// gRPC client config. They mirror the HOOP_TLS_SKIP_VERIFY /
	// HOOP_TLSSERVERNAME env vars the CLI honours.
	TLSSkipVerify bool
	TLSServerName string

	// UserAgent is the User-Agent string the per-flow gRPC dials
	// present to the gateway. Required.
	UserAgent string

	// ResolvedConfigurer drives systemd-resolved registration during
	// BringUp / TearDown. Optional: if nil, New uses
	// resolved.New() (production behaviour — auto-detect, fall
	// back to the manual hint on unsupported hosts). Tests inject a
	// fake to assert call shape without spawning resolvectl.
	ResolvedConfigurer resolved.Configurer
}

// Manager is the lifecycle owner. Construct with New; drive with
// BringUp / TearDown; read with Snapshot. Safe for concurrent use.
type Manager struct {
	opts Options

	mu       sync.RWMutex
	state    State
	since    time.Time
	current  *liveTunnel // non-nil iff state == StateUp
}

// liveTunnel groups every resource that participates in a single
// "up" session. Held under Manager.mu while transitioning; readable
// without the lock once published to Manager.current.
type liveTunnel struct {
	ctx      context.Context
	cancel   context.CancelFunc
	alloc    *addressing.Allocator
	registry *connRegistry
	stack    *netstack.Stack
	deviceName string
	prefix     string
	hostAddr   netip.Addr
	gateway    netip.Addr
	apiBase    string
	// token is the gateway bearer token captured at bring-up, reused by
	// Refresh to re-fetch the connection list without going through the
	// daemon config again. It lives only in memory for the tunnel's
	// lifetime (the daemon's config file is the durable copy).
	token string

	// routeCfg is the exact addressing handed to netstack.ConfigureRoutes
	// at bring-up. Stored verbatim so teardown reverses precisely what was
	// installed (both v4 and v6) without closeTunnel having to re-derive
	// it from the allocator.
	routeCfg netstack.RouteConfig

	// resolvedActive is true iff the systemd-resolved registration
	// for this tunnel succeeded. Drives both the post-bring-up
	// banner (Snapshot.ResolvedConfigured) and the teardown order
	// (we only call resolved.Unconfigure when we know there's
	// per-link state to revert).
	resolvedActive bool

	// resolved is the per-tunnel handle on the systemd-resolved
	// CLI. Held here rather than read via m.opts.ResolvedConfigurer
	// on teardown so closeTunnel — which doesn't have the Manager
	// — can still revert resolved without us passing the manager
	// reference around.
	resolved resolved.Configurer
}

// New constructs a Manager. It does not bind any network resources
// (TUN, sockets, gRPC) — those happen at BringUp.
func New(opts Options) (*Manager, error) {
	if opts.Logger == nil {
		return nil, errors.New("tunnelmgr.New: Logger is required")
	}
	if opts.SessionSeed == "" {
		return nil, errors.New("tunnelmgr.New: SessionSeed is required")
	}
	if opts.TLD == "" {
		return nil, errors.New("tunnelmgr.New: TLD is required")
	}
	if opts.UserAgent == "" {
		return nil, errors.New("tunnelmgr.New: UserAgent is required")
	}
	if opts.ResolvedConfigurer == nil {
		// Production default: auto-detect systemd-resolved. Falls
		// back to the manual-hint banner when the host doesn't
		// run it.
		opts.ResolvedConfigurer = resolved.New()
	}
	return &Manager{opts: opts}, nil
}

// Snapshot returns a copy-on-read view of the current state. Cheap;
// callers may invoke it on the hot path of /v1/status.
func (m *Manager) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.current == nil {
		return Snapshot{State: m.state, Since: m.since}
	}
	return Snapshot{
		State:              m.state,
		Since:              m.since,
		Allocator:          m.current.alloc,
		Connections:        m.current.registry.activeConnections(),
		HostAddr:           m.current.hostAddr,
		Gateway:            m.current.gateway,
		DeviceName:         m.current.deviceName,
		ResolvedConfigured: m.current.resolvedActive,
	}
}

// State returns the current lifecycle position. Equivalent to
// Snapshot().State but cheaper when callers don't need the full
// snapshot.
func (m *Manager) State() State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}

// ErrAlreadyUp is returned by BringUp when called while the tunnel
// is already in StateUp. Callers should TearDown first if they want
// to apply new credentials.
var ErrAlreadyUp = errors.New("tunnelmgr: tunnel is already up")

// BringUp builds a fresh tunnel from the supplied config. cfg.APIURL
// + cfg.Token must be set; cfg.GrpcURL is optional (auto-discovered
// from /api/serverinfo when empty).
//
// Errors leave the manager in StateIdle (no partial bring-up
// persists). Callers can retry after fixing the underlying problem.
//
// parentCtx is the daemon's root context. The internal tunnel
// goroutines bind to a child derived from it; cancelling parentCtx
// implicitly tears down whatever's up.
func (m *Manager) BringUp(parentCtx context.Context, cfg daemonconfig.Config) error {
	m.mu.Lock()
	if m.state == StateUp {
		m.mu.Unlock()
		return ErrAlreadyUp
	}
	m.mu.Unlock()

	// Phase 1: assemble all the per-tunnel state without holding the
	// manager lock so concurrent Snapshot()s can keep working. The
	// lock is taken only at the end to publish the result.
	tunnel, err := m.buildTunnel(parentCtx, cfg)
	if err != nil {
		// Best-effort: nothing partially-applied to undo (buildTunnel
		// owns its own cleanup on error paths).
		return err
	}

	m.mu.Lock()
	// Race window: a concurrent BringUp may have won the slot while we
	// were dialling the gateway. Close our half-built tunnel and
	// surface the conflict so the caller knows to retry.
	if m.state == StateUp {
		m.mu.Unlock()
		closeTunnel(m.opts.Logger, tunnel)
		return ErrAlreadyUp
	}
	m.current = tunnel
	m.state = StateUp
	m.since = time.Now()
	m.mu.Unlock()

	// Watch the parent context: if the daemon shuts down, the netstack
	// goroutines exit and we want to clear our published state so a
	// late Snapshot() doesn't claim Up against a dead Stack.
	go m.watchParent(tunnel)

	return nil
}

// TearDown closes the active tunnel and returns the manager to Idle.
// Safe to call when already idle; nil-returns in that case.
func (m *Manager) TearDown() error {
	m.mu.Lock()
	if m.state == StateIdle {
		m.mu.Unlock()
		return nil
	}
	old := m.current
	m.current = nil
	m.state = StateIdle
	m.since = time.Time{}
	m.mu.Unlock()

	closeTunnel(m.opts.Logger, old)
	return nil
}

// watchParent is one tiny goroutine per BringUp. It exists to
// transition the manager back to Idle when the daemon's root context
// cancels — without it, a `systemctl stop` would close the TUN device
// (via Stack.Close) but leave the manager reporting StateUp via
// Snapshot until the process exits, which would confuse any
// last-second IPC clients.
func (m *Manager) watchParent(t *liveTunnel) {
	<-t.ctx.Done()
	// Only own-up the transition if this tunnel is still the published
	// one — if TearDown already swapped it out we have nothing to do.
	m.mu.Lock()
	if m.current == t {
		m.current = nil
		m.state = StateIdle
		m.since = time.Time{}
	}
	m.mu.Unlock()
}

// closeTunnel runs the full teardown for a previously-published
// tunnel. Idempotent — safe to call twice; the second pass is a
// no-op because cancel() and Close() handle re-entry gracefully.
func closeTunnel(logger Logger, t *liveTunnel) {
	if t == nil {
		return
	}
	// Order matters: cancel the per-tunnel context first so any
	// in-flight gRPC pipes see the deadline and exit cleanly,
	// then revert systemd-resolved (while the interface still
	// exists — easier for resolvectl), then take the TUN device
	// offline (netstack.UnconfigureRoutes) so the host kernel
	// stops sending us packets, and finally Close the stack so
	// the goroutines reading from the TUN fd drain.
	if t.cancel != nil {
		t.cancel()
	}
	// Revert resolved BEFORE the netstack tears the link down so
	// `resolvectl revert <iface>` is operating on a live link
	// (cleaner journal output). resolvedActive guards us from
	// calling revert on a link we never wired up.
	if t.resolvedActive && t.resolved != nil && t.deviceName != "" {
		if err := t.resolved.Unconfigure(t.deviceName); err != nil {
			// Best-effort: log and continue. A failed revert
			// leaves at worst a stale per-link entry that
			// auto-clears when systemd-resolved sees the
			// interface disappear in the next few lines.
			logger.Printf("tunnelmgr: resolved unconfigure: %v", err)
		}
	}
	// UnconfigureRoutes only runs once buildTunnel has actually
	// installed them — routeCfg is populated together with the rest of
	// the live-tunnel state right before buildTunnel returns success, so
	// a non-empty Device implies the routes are real.
	if t.routeCfg.Device != "" {
		netstack.UnconfigureRoutes(t.routeCfg)
	}
	if t.stack != nil {
		if err := t.stack.Close(); err != nil {
			logger.Printf("tunnelmgr: stack close: %v", err)
		}
	}
}

// ----------------------------------------------------------------------
// build pipeline
// ----------------------------------------------------------------------

// buildTunnel performs the side-effectful work of BringUp without
// touching m.mu. On any error it cleans up whatever was partially
// allocated and returns the original failure.
func (m *Manager) buildTunnel(parentCtx context.Context, cfg daemonconfig.Config) (*liveTunnel, error) {
	if cfg.APIURL == "" || cfg.Token == "" {
		return nil, errors.New("tunnelmgr: BringUp requires APIURL and Token in config")
	}

	ctx, cancel := context.WithCancel(parentCtx)
	cleanup := func(err error) (*liveTunnel, error) {
		cancel()
		return nil, err
	}

	gatewayCfg, apiBase, err := m.buildGatewayConfig(ctx, cfg)
	if err != nil {
		return cleanup(err)
	}
	m.opts.Logger.Printf("tunnelmgr: gateway gRPC %s (insecure=%v) api %s",
		gatewayCfg.ServerAddress, gatewayCfg.Insecure, apiBase)

	alloc := addressing.New(m.opts.SessionSeed)
	m.opts.Logger.Printf("tunnelmgr: session prefix %s gateway %s",
		alloc.Prefix(), alloc.Gateway())

	registry := newConnRegistry()
	added, err := loadConnections(ctx, alloc, registry, apiBase, gatewayCfg.Token, m.opts.Logger)
	if err != nil {
		return cleanup(err)
	}
	if added == 0 {
		return cleanup(errors.New("no tunnelable connections found for this user"))
	}

	rsvr := resolver.New(alloc, m.opts.TLD)
	stack, err := netstack.New(ctx, netstack.Options{
		Prefix:     alloc.Prefix(),
		Gateway:    alloc.Gateway(),
		PrefixV4:   alloc.PrefixV4(),
		GatewayV4:  alloc.GatewayV4(),
		DeviceName: m.opts.DeviceName,
		TCPAccept:  m.makeAcceptFunc(alloc, registry),
		TCPHandler: m.makeTCPHandler(alloc, registry, gatewayCfg),
		DNSHandler: rsvr.HandleUDP,
	})
	if err != nil {
		return cleanup(fmt.Errorf("netstack: %w", err))
	}

	deviceName := stack.DeviceName()
	routeCfg := netstack.RouteConfig{
		Device:     deviceName,
		Prefix:     alloc.Prefix().String(),
		HostAddr:   alloc.HostAddr().String(),
		PrefixV4:   alloc.PrefixV4().String(),
		HostAddrV4: alloc.HostAddrV4().String(),
	}
	if err := netstack.ConfigureRoutes(routeCfg); err != nil {
		_ = stack.Close()
		return cleanup(fmt.Errorf("configure routes: %w", err))
	}

	// Register the tunnel's per-link DNS with systemd-resolved.
	// Best-effort: on hosts without resolved (or on a resolvectl
	// error) we log the outcome and let the operator fall back to
	// the manual hint the banner already prints. Failing this
	// step MUST NOT block the tunnel coming up — the per-flow
	// gRPC plumbing still works the moment the user runs `dig
	// @<gateway>` manually.
	resolvedActive := false
	resolvedCfg := resolved.Config{
		Device:       deviceName,
		DNSAddress:   alloc.Gateway().String(),
		SearchDomain: m.opts.TLD,
	}
	switch err := m.opts.ResolvedConfigurer.Configure(resolvedCfg); {
	case err == nil:
		resolvedActive = true
		m.opts.Logger.Printf("tunnelmgr: systemd-resolved wired up: dns=%s domain=~%s iface=%s",
			resolvedCfg.DNSAddress, resolvedCfg.SearchDomain, resolvedCfg.Device)
	case errors.Is(err, resolved.ErrUnsupported):
		// Quiet info: a non-resolved host is the common case on
		// Alpine, FreeBSD, etc. The banner block printed in
		// main.go covers the "here's how to do it manually" case.
		m.opts.Logger.Printf("tunnelmgr: systemd-resolved not present; printing manual DNS hint instead")
	default:
		m.opts.Logger.Printf("tunnelmgr: systemd-resolved registration failed: %v (falling back to manual hint)", err)
	}

	m.opts.Logger.Printf("tunnelmgr: tunnel up — device=%s gateway=%s host=%s",
		deviceName, alloc.Gateway(), alloc.HostAddr())

	return &liveTunnel{
		ctx:            ctx,
		cancel:         cancel,
		alloc:          alloc,
		registry:       registry,
		stack:          stack,
		deviceName:     deviceName,
		prefix:         alloc.Prefix().String(),
		hostAddr:       alloc.HostAddr(),
		gateway:        alloc.Gateway(),
		apiBase:        apiBase,
		token:          gatewayCfg.Token,
		routeCfg:       routeCfg,
		resolvedActive: resolvedActive,
		resolved:       m.opts.ResolvedConfigurer,
	}, nil
}

// loadConnections fetches the tunnelable connection list from the
// gateway, allocates a stable IP for every name (append-only — existing
// allocations are untouched and re-adds are no-ops), and reconciles the
// registry's active set to exactly the fetched list. Returns the number
// of currently-active connections (i.e. len of the fetched list on
// success).
//
// Shared by buildTunnel (initial load) and Refresh (periodic / manual
// re-load) so both paths apply identical filtering and allocation
// semantics. The allocator's determinism means a name that vanished and
// later reappears regains its original IP.
func loadConnections(
	ctx context.Context,
	alloc *addressing.Allocator,
	registry *connRegistry,
	apiBase, token string,
	logger Logger,
) (activeCount int, err error) {
	conns, err := client.FetchConnections(ctx, client.FetchConnectionsOptions{
		APIBaseURL: apiBase,
		Token:      token,
	})
	if err != nil {
		return 0, fmt.Errorf("fetch connections: %w", err)
	}

	active := make(map[string]string, len(conns))
	for _, c := range conns {
		if _, err := alloc.AddName(c.Name); err != nil {
			return 0, fmt.Errorf("allocate %s: %w", c.Name, err)
		}
		active[c.Name] = c.SubType
	}

	added, removed := registry.reconcile(active)
	logger.Printf("tunnelmgr: connection list synced — %d active (%d new, %d retired)",
		len(active), added, removed)
	return len(active), nil
}

// Refresh re-fetches the connection list and reconciles it into the
// live tunnel without disturbing the netstack, routes, or in-flight
// flows. New connections become routable immediately (the allocator and
// resolver hold live references); connections deleted on the gateway
// are marked inactive (hidden from listings, new SYNs rejected) but
// keep their reserved IP.
//
// No-op (returns nil) when the tunnel is not up — there is nothing to
// refresh against and the periodic caller may race a teardown.
func (m *Manager) Refresh(ctx context.Context) error {
	m.mu.RLock()
	t := m.current
	m.mu.RUnlock()
	if t == nil {
		return nil
	}
	_, err := loadConnections(ctx, t.alloc, t.registry, t.apiBase, t.token, m.opts.Logger)
	return err
}

// Compile-time assertion: *log.Logger satisfies our Logger interface.
var _ Logger = (*log.Logger)(nil)
