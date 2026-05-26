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
	// nil when idle. The IPC layer iterates it to build /v1/connections.
	Allocator *addressing.Allocator

	// SubTypeByName maps connection name → hoop subtype ("postgres",
	// "mysql", ...). Keyed by the same names the allocator holds.
	// nil when idle.
	SubTypeByName map[string]string

	// HostAddr / Gateway are the addresses the kernel and gVisor own
	// on the current session's /48. Zero when idle.
	HostAddr netip.Addr
	Gateway  netip.Addr

	// DeviceName is the resolved TUN device name (e.g. "tun0").
	// Empty when idle.
	DeviceName string
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
	ctx        context.Context
	cancel     context.CancelFunc
	alloc      *addressing.Allocator
	subTypeBy  map[string]string
	stack      *netstack.Stack
	deviceName string
	prefix     string
	hostAddr   netip.Addr
	gateway    netip.Addr
	apiBase    string
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
		State:         m.state,
		Since:         m.since,
		Allocator:     m.current.alloc,
		SubTypeByName: m.current.subTypeBy,
		HostAddr:      m.current.hostAddr,
		Gateway:       m.current.gateway,
		DeviceName:    m.current.deviceName,
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
	// in-flight gRPC pipes see the deadline and exit cleanly, then
	// take the TUN device offline (netstack.UnconfigureRoutes) so the
	// host kernel stops sending us packets, and finally Close the
	// stack so the goroutines reading from the TUN fd drain.
	if t.cancel != nil {
		t.cancel()
	}
	// UnconfigureRoutes only runs once buildTunnel has actually
	// installed them — the deviceName + prefix + hostAddr fields are
	// set together right before the function returns success, so any
	// non-zero hostAddr implies the routes are real.
	if t.deviceName != "" && t.hostAddr.IsValid() {
		netstack.UnconfigureRoutes(t.deviceName, t.prefix, t.hostAddr.String())
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

	conns, err := client.FetchConnections(ctx, client.FetchConnectionsOptions{
		APIBaseURL: apiBase,
		Token:      gatewayCfg.Token,
	})
	if err != nil {
		return cleanup(fmt.Errorf("fetch connections: %w", err))
	}
	if len(conns) == 0 {
		return cleanup(errors.New("no tunnelable connections found for this user"))
	}

	subTypeByName := make(map[string]string, len(conns))
	for _, c := range conns {
		if _, err := alloc.AddName(c.Name); err != nil {
			return cleanup(fmt.Errorf("allocate %s: %w", c.Name, err))
		}
		subTypeByName[c.Name] = c.SubType
	}
	m.opts.Logger.Printf("tunnelmgr: loaded %d tunnelable connection(s)", len(conns))

	rsvr := resolver.New(alloc, m.opts.TLD)
	stack, err := netstack.New(ctx, netstack.Options{
		Prefix:     alloc.Prefix(),
		Gateway:    alloc.Gateway(),
		DeviceName: m.opts.DeviceName,
		TCPAccept:  m.makeAcceptFunc(alloc, subTypeByName),
		TCPHandler: m.makeTCPHandler(alloc, subTypeByName, gatewayCfg),
		DNSHandler: rsvr.HandleUDP,
	})
	if err != nil {
		return cleanup(fmt.Errorf("netstack: %w", err))
	}

	deviceName := stack.DeviceName()
	if err := netstack.ConfigureRoutes(deviceName, alloc.Prefix().String(), alloc.HostAddr().String()); err != nil {
		_ = stack.Close()
		return cleanup(fmt.Errorf("configure routes: %w", err))
	}

	m.opts.Logger.Printf("tunnelmgr: tunnel up — device=%s gateway=%s host=%s",
		deviceName, alloc.Gateway(), alloc.HostAddr())

	return &liveTunnel{
		ctx:        ctx,
		cancel:     cancel,
		alloc:      alloc,
		subTypeBy:  subTypeByName,
		stack:      stack,
		deviceName: deviceName,
		prefix:     alloc.Prefix().String(),
		hostAddr:   alloc.HostAddr(),
		gateway:    alloc.Gateway(),
		apiBase:    apiBase,
	}, nil
}

// Compile-time assertion: *log.Logger satisfies our Logger interface.
var _ Logger = (*log.Logger)(nil)
