// Package ica links the libhoop-ica engine into a sidecar binary as the
// server behind an ica lane. daemon/ica.go defines the contract: the daemon
// resolves config into an ICALane and an ICARuntime, this package turns
// them into the engine's Runtime, Manager and transparent listener, and
// returns the readiness, statistics and session list the admin API
// serves.
//
// Validate calls Build on the control plane and on a developer's macOS, so
// Build resolves the runtime profile, checks it with the engine's validator
// and inspects the lane. Build binds no socket, resolves no name and loads
// no model; Serve does those.
package ica

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/libhoop-ica/ica"
)

func init() { daemon.RegisterICAEngine(Build) }

// These failure diagnostics file bounds match the standalone agent's, so
// metadata-only failure files look the same whichever binary wrote them.
const (
	failureFileBytes  = 4 << 20
	failureTotalBytes = 32 << 20
)

// drainTimeout bounds how long Close waits for active sessions to end
// before it stops the manager, which cancels the sessions still running.
const drainTimeout = 30 * time.Second

// Build implements daemon.ICABuilder.
func Build(rt daemon.ICARuntime, lane daemon.ICALane) (daemon.ICAServer, error) {
	cfg := resolveRuntimeConfig(rt)
	if err := ica.ValidateRuntimeConfig(cfg); err != nil {
		return nil, fmt.Errorf("ica options: %w", err)
	}
	if err := validateLane(lane); err != nil {
		return nil, err
	}
	return &server{cfg: cfg, rt: rt, lane: lane, notes: notes(cfg, lane)}, nil
}

// override copies src onto dst when src is set; a nil src keeps the
// profile's value.
func override[T any](dst *T, src *T) {
	if src != nil {
		*dst = *src
	}
}

// resolveRuntimeConfig lays the operator's paths and overrides over the
// engine's production profile. The override surface excludes OCRBackend
// and BitmapDecoder, so the sidecar runs the profile's
// tesseract/independent pair. Serve opens the FailureDiagnostics writer, so
// this function leaves it unset.
func resolveRuntimeConfig(rt daemon.ICARuntime) ica.RuntimeConfig {
	cfg := ica.DefaultRuntimeConfig()
	for _, p := range [...]struct {
		dst *string
		src string
	}{{&cfg.OCRWorker, rt.OCRWorker}, {&cfg.Tessdata, rt.Tessdata}, {&cfg.NERModel, rt.NERModel}, {&cfg.NERORTLibrary, rt.NERORTLibrary}} {
		if p.src != "" {
			*p.dst = p.src
		}
	}
	o := rt.Options
	override(&cfg.OCRWorkers, o.OCRWorkers)
	override(&cfg.OCRTiles, o.OCRTiles)
	override(&cfg.OCRThreadLimit, o.OCRThreadLimit)
	if o.OCRTimeoutSec != nil {
		cfg.OCRTimeout = time.Duration(*o.OCRTimeoutSec) * time.Second
	}
	override(&cfg.OCRMinConfidence, o.OCRMinConfidence)
	override(&cfg.OCRBrowserOptimizations, o.OCRBrowserOptimizations)
	override(&cfg.OCRScreenOptimizations, o.OCRScreenOptimizations)
	override(&cfg.OCRGrayPreparation, o.OCRGrayPreparation)
	override(&cfg.OCRNoInvert, o.OCRNoInvert)
	override(&cfg.OCRFreshReuseFrames, o.OCRFreshReuseFrames)
	override(&cfg.OCRLineBandScale, o.OCRLineBandScale)
	override(&cfg.OCRLineBandMinRows, o.OCRLineBandMinRows)
	override(&cfg.NERBackend, o.NERBackend)
	override(&cfg.NERThreads, o.NERThreads)
	override(&cfg.NERParallel, o.NERParallel)
	override(&cfg.PIIThreshold, o.PIIThreshold)
	if len(o.PIIEntities) > 0 {
		cfg.PIIEntities = slices.Clone(o.PIIEntities)
	}
	override(&cfg.VideoBatch, o.VideoBatch)
	override(&cfg.OutputPipeline, o.OutputPipeline)
	override(&cfg.AdaptiveCoalescing, o.AdaptiveCoalescing)
	override(&cfg.PresentationV2, o.PresentationV2)
	override(&cfg.PresentationScreenCopy, o.PresentationScreenCopy)
	override(&cfg.OutputLatestFrame, o.OutputLatestFrame)
	return cfg
}

// validateLane checks what the daemon's config validation cannot see
// without the engine's types. It accepts CIDR mode on a non-Linux host so
// -validate passes on a developer's machine; ica.Listen refuses it at
// Serve.
func validateLane(lane daemon.ICALane) error {
	if strings.TrimSpace(lane.TenantID) == "" {
		return errors.New("ica.tenant_id is required")
	}
	if lane.Transparent {
		if len(lane.UpstreamPrefixes) == 0 {
			return errors.New("ica.transparent.upstream_cidrs is empty: no VDA could ever be admitted")
		}
		return nil
	}
	if _, _, err := splitUpstream(lane.Upstream); err != nil {
		return err
	}
	return nil
}

// splitUpstream parses the single-VDA upstream as host:port with a port in
// 1..65535, since the engine admits only a literal ip:port.
func splitUpstream(upstream string) (host string, port uint16, err error) {
	host, portText, err := net.SplitHostPort(upstream)
	if err != nil {
		return "", 0, fmt.Errorf("upstream must be host:port: %w", err)
	}
	p, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || p == 0 {
		return "", 0, fmt.Errorf("upstream %q: port must be 1..65535", upstream)
	}
	return host, uint16(p), nil
}

// resolveUpstream turns the single-VDA upstream into one address, once. A
// literal passes through; the system resolver resolves a hostname and the
// first answer wins. The engine dials literals only, and a policy that
// re-resolved per connection could admit a different host than the one the
// operator validated.
func resolveUpstream(ctx context.Context, upstream string) (netip.Addr, uint16, error) {
	host, port, err := splitUpstream(upstream)
	if err != nil {
		return netip.Addr{}, 0, err
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.Unmap(), port, nil
	}
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return netip.Addr{}, 0, fmt.Errorf("resolve upstream %q: %w", host, err)
	}
	if len(addrs) == 0 {
		return netip.Addr{}, 0, fmt.Errorf("resolve upstream %q: no address", host)
	}
	return addrs[0].Unmap(), port, nil
}

// singlePolicy is the admission policy for one address: that host (/32 or
// /128) on that port, plus the fixed destination string for accepted
// connections.
func singlePolicy(addr netip.Addr, port uint16) (ica.NetworkPolicy, string) {
	addr = addr.Unmap()
	policy := ica.NetworkPolicy{Prefixes: []netip.Prefix{netip.PrefixFrom(addr, addr.BitLen())},
		Ports: map[uint16]struct{}{port: {}}}
	return policy, netip.AddrPortFrom(addr, port).String()
}

// cidrPolicy is CIDR mode's admission policy as the operator wrote it.
func cidrPolicy(lane daemon.ICALane) ica.NetworkPolicy {
	policy := ica.NetworkPolicy{Prefixes: slices.Clone(lane.UpstreamPrefixes)}
	if len(lane.UpstreamPorts) > 0 {
		policy.Ports = make(map[uint16]struct{}, len(lane.UpstreamPorts))
		for _, p := range lane.UpstreamPorts {
			policy.Ports[p] = struct{}{}
		}
	}
	return policy
}

// anySource is single mode's source policy when the lane names none. The
// engine requires a non-empty list, and a single-VDA lane admits any client
// that reaches the listener, as the other sidecar lanes do.
var anySource = []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}

// notes lists the resolved runtime values a -validate run reports. Only
// this side of the seam knows them: the operator writes overrides, and the
// profile they override lives in the engine. The daemon's icaLaneNotes
// reports the mode and the CIDR-mode privileges, since the daemon can state
// them without an engine, for the control plane.
func notes(cfg ica.RuntimeConfig, lane daemon.ICALane) []string {
	out := []string{
		fmt.Sprintf("ica: ocr tesseract, %d workers, %d tiles, thread limit %d, timeout %s, min confidence %.2f",
			cfg.OCRWorkers, cfg.OCRTiles, cfg.OCRThreadLimit, cfg.OCRTimeout, cfg.OCRMinConfidence),
		fmt.Sprintf("ica: ner backend %s (onnx runtime compiled in: %t), %d parallel, %d threads",
			cfg.NERBackend, ica.ORTCompiled, cfg.NERParallel, cfg.NERThreads),
		fmt.Sprintf("ica: pii threshold %.2f, entities %s", cfg.PIIThreshold, strings.Join(cfg.PIIEntities, ",")),
		fmt.Sprintf("ica: video batch %t, output pipeline %t, adaptive coalescing %t, presentation v2 %t, screen copy %t, latest frame %t",
			cfg.VideoBatch, cfg.OutputPipeline, cfg.AdaptiveCoalescing, cfg.PresentationV2, cfg.PresentationScreenCopy, cfg.OutputLatestFrame),
		fmt.Sprintf("ica: ocr worker %s, tessdata %s, ner model %s, ort library %s",
			cfg.OCRWorker, cfg.Tessdata, cfg.NERModel, cfg.NERORTLibrary),
	}
	if lane.Transparent && runtime.GOOS != "linux" {
		out = append(out, "ica: this host is not Linux and cannot serve CIDR mode; -validate passes, Serve will not")
	}
	return out
}

// sharedRuntime is one loaded engine runtime and the lanes holding it.
// NewRuntime loads a model and spawns OCR workers, which costs minutes and
// gigabytes, so lanes with the same resolved RuntimeConfig share one
// runtime. Lanes with different options get separate runtimes and pay a
// second load; the daemon's config has one `ica` block, so the current
// config format does not produce that case.
type sharedRuntime struct {
	key  string
	done chan struct{} // closed when load finishes; runtime and err are valid after

	runtime     *ica.Runtime
	diagnostics *ica.FailureDiagnostics
	err         error

	// refs and loaded are guarded by runtimesMu.
	refs   int
	loaded bool
}

var (
	runtimesMu sync.Mutex
	runtimes   = map[string]*sharedRuntime{}
)

// fingerprint identifies a resolved RuntimeConfig for sharing. It sorts
// PIIEntities so two identical sets in different order share a runtime.
func fingerprint(cfg ica.RuntimeConfig, diagnosticDir string) string {
	cfg.PIIEntities = slices.Clone(cfg.PIIEntities)
	slices.Sort(cfg.PIIEntities)
	return fmt.Sprintf("%+v|%s", cfg, diagnosticDir)
}

// acquireRuntime returns the runtime for cfg, loading it on first use. The
// first caller loads; the others wait for that load or for their own ctx.
func acquireRuntime(ctx context.Context, cfg ica.RuntimeConfig, diagnosticDir string) (*sharedRuntime, error) {
	key := fingerprint(cfg, diagnosticDir)
	runtimesMu.Lock()
	entry, loading := runtimes[key]
	if !loading {
		entry = &sharedRuntime{key: key, done: make(chan struct{})}
		runtimes[key] = entry
	}
	entry.refs++
	runtimesMu.Unlock()

	if !loading {
		entry.load(ctx, cfg, diagnosticDir)
	}
	select {
	case <-entry.done:
	case <-ctx.Done():
		releaseRuntime(entry)
		return nil, ctx.Err()
	}
	if entry.err != nil {
		releaseRuntime(entry)
		return nil, entry.err
	}
	return entry, nil
}

func (e *sharedRuntime) load(ctx context.Context, cfg ica.RuntimeConfig, diagnosticDir string) {
	defer close(e.done)
	if diagnosticDir != "" {
		e.diagnostics, e.err = ica.OpenFailureDiagnostics(diagnosticDir, failureFileBytes, failureTotalBytes)
		if e.err != nil {
			e.err = fmt.Errorf("open failure diagnostics: %w", e.err)
		}
	}
	if e.err == nil {
		cfg.FailureDiagnostics = e.diagnostics
		e.runtime, e.err = ica.NewRuntime(ctx, cfg)
	}
	runtimesMu.Lock()
	defer runtimesMu.Unlock()
	e.loaded = true
	if e.err != nil || e.refs == 0 {
		// No lane holds it: every acquirer gave up during the load, or the
		// load failed. Removing the entry keeps the next acquirer from
		// getting a dead runtime.
		delete(runtimes, e.key)
		e.closeResources()
	}
}

// releaseRuntime drops one lane's hold; the last one closes the runtime.
func releaseRuntime(e *sharedRuntime) {
	runtimesMu.Lock()
	defer runtimesMu.Unlock()
	e.refs--
	if e.refs > 0 || !e.loaded || runtimes[e.key] != e {
		return
	}
	delete(runtimes, e.key)
	e.closeResources()
}

// closeResources runs under runtimesMu. It closes the runtime first, so no
// worker writes a failure file after the writer closes.
func (e *sharedRuntime) closeResources() {
	if e.runtime != nil {
		_ = e.runtime.Close()
		e.runtime = nil
	}
	if e.diagnostics != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = e.diagnostics.Close(ctx)
		cancel()
		e.diagnostics = nil
	}
}

// server is one ica lane. The fields after cfg/rt/lane/notes are in use
// only between Serve and Close.
type server struct {
	cfg   ica.RuntimeConfig
	rt    daemon.ICARuntime
	lane  daemon.ICALane
	notes []string

	// denied counts refused connections, which never became sessions, plus
	// sessions the pipeline blocked. The engine's Stats count neither.
	denied atomic.Int64

	mu       sync.Mutex
	closed   bool
	shared   *sharedRuntime
	manager  *ica.Manager
	listener net.Listener
}

// Serve loads the runtime (which takes minutes), resolves the destination
// policy, binds the listener and runs the engine's accept loop until the
// listener closes or ctx ends; Serve returns nil in both cases. Serve also
// returns nil when a shutdown arrives during the load.
func (s *server) Serve(ctx context.Context) error {
	if s.isClosed() {
		return nil
	}
	shared, err := acquireRuntime(ctx, s.cfg, s.rt.FailureDiagnosticDir)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("%s: load ica runtime: %w", s.lane.Name, err)
	}
	if s.isClosed() {
		releaseRuntime(shared)
		return nil
	}
	manager, listener, tc, summary, err := s.start(ctx, shared)
	if err != nil {
		releaseRuntime(shared)
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("%s: %w", s.lane.Name, err)
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = listener.Close()
		_ = manager.Close()
		releaseRuntime(shared)
		return nil
	}
	s.shared, s.manager, s.listener = shared, manager, listener
	s.mu.Unlock()

	if s.lane.Log != nil {
		s.lane.Log.Info("ica lane ready", "listen", listener.Addr().String(), "mode", s.mode(), "policy", summary,
			"tenant", s.lane.TenantID)
	}
	return manager.ServeTransparent(listener, tc)
}

func (s *server) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *server) mode() string {
	if s.lane.Transparent {
		return "cidr"
	}
	return "single"
}

// start builds the manager and the listener for one Serve. It builds the
// manager first, because a bound socket with no manager behind it would
// accept connections and drop them.
func (s *server) start(ctx context.Context, shared *sharedRuntime) (*ica.Manager, net.Listener, ica.TransparentConfig, string, error) {
	tc := ica.TransparentConfig{TenantID: s.lane.TenantID, Width: s.lane.Width, Height: s.lane.Height,
		ThinwireChannel: s.lane.ThinwireChannel}
	var (
		policy  ica.NetworkPolicy
		summary string
	)
	if s.lane.Transparent {
		policy = cidrPolicy(s.lane)
		tc.SourcePrefixes = s.lane.SourcePrefixes
		tc.ResolveDestination = ica.OriginalDestination
		summary = fmt.Sprintf("%v ports %v", s.lane.UpstreamPrefixes, s.lane.UpstreamPorts)
	} else {
		addr, port, err := resolveUpstream(ctx, s.lane.Upstream)
		if err != nil {
			return nil, nil, tc, "", err
		}
		var fixed string
		policy, fixed = singlePolicy(addr, port)
		tc.SourcePrefixes = s.lane.SourcePrefixes
		if len(tc.SourcePrefixes) == 0 {
			tc.SourcePrefixes = anySource
		}
		tc.ResolveDestination = func(net.Conn) (string, error) { return fixed, nil }
		summary = fixed
	}

	manager, err := ica.NewManager(ctx, ica.ManagerConfig{
		Runtime: shared.runtime, NetworkPolicy: policy, MaxSessions: s.rt.MaxSessions,
		ConnectTimeout:     time.Duration(s.rt.ConnectTimeoutSec) * time.Second,
		DisableAuditOutput: true, EventHook: s.observe,
	})
	if err != nil {
		return nil, nil, tc, "", fmt.Errorf("ica manager: %w", err)
	}
	listener, err := s.listen(ctx)
	if err != nil {
		_ = manager.Close()
		return nil, nil, tc, "", err
	}
	return manager, listener, tc, summary, nil
}

// listen binds the lane's socket: IP_TRANSPARENT in CIDR mode, so an
// accepted connection keeps its original destination; a plain socket in
// single mode, where the destination is fixed.
func (s *server) listen(ctx context.Context) (net.Listener, error) {
	if s.lane.Transparent {
		return ica.Listen(ctx, s.lane.Listen)
	}
	network := "tcp"
	if s.lane.Network == "unix" {
		network = "unix"
	}
	return (&net.ListenConfig{}).Listen(ctx, network, s.lane.Listen)
}

// observe is the engine's EventHook. The engine calls it on a worker under
// the sink lock, so observe only counts and forwards. The lane's Events is
// the daemon's audit bridge, which the daemon built to run in that position.
func (s *server) observe(e ica.Event) {
	if e.Event == ica.EventConnectionRejected || (e.Event == ica.EventSessionEnd && e.Data["decision"] == ica.DecisionBlock) {
		s.denied.Add(1)
	}
	if s.lane.Events != nil {
		s.lane.Events(daemon.ICAEvent{Time: e.Time, Name: e.Event, SessionID: e.SessionID, Direction: e.Direction, Data: e.Data})
	}
}

// Close stops intake, waits a bounded time for the sessions in flight,
// stops the manager and drops the lane's hold on the shared runtime. Close
// is idempotent and safe before Serve or during Serve's load: Serve checks
// the closed flag before it installs what it built.
func (s *server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	shared, manager, listener := s.shared, s.manager, s.listener
	s.mu.Unlock()
	if manager == nil {
		return nil
	}
	err := listener.Close()
	if errors.Is(err, net.ErrClosed) {
		err = nil
	}
	deadline := time.Now().Add(drainTimeout)
	for manager.Stats().Active > 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	err = errors.Join(err, manager.Close())
	releaseRuntime(shared)
	return err
}

func (s *server) Addr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

func (s *server) Stats() (active, total, denied int64) {
	s.mu.Lock()
	manager := s.manager
	s.mu.Unlock()
	if manager == nil {
		return 0, 0, s.denied.Load()
	}
	st := manager.Stats()
	return int64(st.Active), int64(st.SessionsTotal), s.denied.Load()
}

func (s *server) Notes() []string { return slices.Clone(s.notes) }

// Ready reports the engine's readiness: the runtime has loaded and its OCR
// workers and NER model answer. Ready returns false before Serve, during
// the load, and after Close.
func (s *server) Ready() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.manager != nil && s.manager.Ready()
}

func (s *server) Sessions() []daemon.ICASessionView {
	s.mu.Lock()
	manager := s.manager
	s.mu.Unlock()
	if manager == nil {
		return nil
	}
	views := manager.List()
	out := make([]daemon.ICASessionView, 0, len(views))
	for _, v := range views {
		out = append(out, daemon.ICASessionView{ID: v.ID, TenantID: v.TenantID, Upstream: v.Upstream, Status: v.Status,
			LastError: v.LastError, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt})
	}
	return out
}

func (s *server) Stop(id string) bool {
	s.mu.Lock()
	manager := s.manager
	s.mu.Unlock()
	if manager == nil {
		return false
	}
	_, ok := manager.Stop(id)
	return ok
}
