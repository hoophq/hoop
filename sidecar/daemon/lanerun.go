package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/identity/token"
	"github.com/hoophq/hoop/sidecar/proxy"
)

// This file turns a lane into a running server and keeps the one list of
// those servers. Run used to hold the list as parallel slices built once;
// a reload that adds or removes a listener (ADR-0014, amended) grows and
// shrinks it while /stats, /readyz, the usage ticker and the shutdown loop
// read it, so the list is a registry under a mutex, and the build and bind
// steps are methods the startup path and the reload path share. A lane a
// reload starts is the lane startup would have built from the same
// document.

// bindWait bounds how long startBound waits for an endpoint lane a reload
// added to publish its bound address. The engines a reload may add (grpc,
// ssh, mcp) bind at once; the wait only catches one that hangs before
// binding, so the reload does not hang with it.
const bindWait = 10 * time.Second

// errNoBind is startBound's deadline: the engine did not bind in bindWait.
var errNoBind = errors.New("did not bind")

// runningLane is one lane as the process runs it: the server, how a reload
// swaps its rules, and the facts the stats zip and the usage event read.
type runningLane struct {
	name     string
	protocol string
	// relay is set for a relay lane (postgres, mysql, http, ...); endpoint
	// for one that terminates the protocol in-process (grpc, spanner, ssh,
	// ica, mcp). One of the two is set, the other nil.
	relay    *proxy.Server
	endpoint endpointServer
	rules    ruleSwapper
	// tokens is the lane's caller_token file, nil without the block. stop
	// uses it to drop the file from the token watcher's list.
	tokens *token.File
	// stop sets stopped before Close, so the serve goroutine does not
	// report the Close as a listener failure.
	stopped *atomic.Bool
	// cancel ends the context the lane serves under. launch sets it and
	// stop calls it, so what Serve started for the lane ends with the
	// lane and not with the process.
	cancel context.CancelFunc
}

func (l runningLane) addr() net.Addr {
	if l.relay != nil {
		return l.relay.Addr()
	}
	return l.endpoint.Addr()
}

func (l runningLane) stats() (active, total, denied int64) {
	if l.relay != nil {
		return l.relay.Stats()
	}
	return l.endpoint.Stats()
}

func (l runningLane) close() error {
	if l.relay != nil {
		return l.relay.Close()
	}
	return l.endpoint.Close()
}

func (l runningLane) serve(ctx context.Context) error {
	if l.relay != nil {
		return l.relay.Serve(ctx)
	}
	return l.endpoint.Serve(ctx)
}

// laneSet is the registry of running lanes, in config order. Readers take
// a snapshot; laneRuntime.startBound and stop are the reloader's, closeAll
// is Run's shutdown, after which a start refuses: a lane the reloader
// starts during shutdown would outlive the WaitGroup that joins the others.
type laneSet struct {
	mu     sync.Mutex
	lanes  []runningLane
	closed bool
}

func (s *laneSet) snapshot() []runningLane {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]runningLane, len(s.lanes))
	copy(out, s.lanes)
	return out
}

// isClosed reports that closeAll ran: the process is stopping, and /readyz
// says so before the first lane closes.
func (s *laneSet) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *laneSet) remove(name string) (runningLane, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, l := range s.lanes {
		if l.name == name {
			s.lanes = append(s.lanes[:i:i], s.lanes[i+1:]...)
			return l, true
		}
	}
	return runningLane{}, false
}

// endpoints returns the endpoint lanes and their names in lockstep, the
// shape /readyz and the ICA drain read.
func (s *laneSet) endpoints() ([]endpointServer, []string) {
	var eps []endpointServer
	var names []string
	for _, l := range s.snapshot() {
		if l.endpoint != nil {
			eps = append(eps, l.endpoint)
			names = append(names, l.name)
		}
	}
	return eps, names
}

// relayCount is how many relay lanes run: the ICA drain refuses a process
// that is not ICA-only, and /readyz's attestation reports it.
func (s *laneSet) relayCount() int {
	n := 0
	for _, l := range s.snapshot() {
		if l.relay != nil {
			n++
		}
	}
	return n
}

func (s *laneSet) statSources() []statSource {
	lanes := s.snapshot()
	out := make([]statSource, 0, len(lanes))
	for _, l := range lanes {
		out = append(out, statSource{l.protocol, l})
	}
	return out
}

// Stats makes runningLane a statSource stats provider.
func (l runningLane) Stats() (active, total, denied int64) { return l.stats() }

// closeAll closes every lane, together, and refuses later starts: an ica
// lane's Close drains its sessions for up to 30s, and closing lanes one
// after another would add those windows up past the pod's termination
// grace period. The lanes stay in the registry: the last usage window is
// cut after this and reads their totals.
func (s *laneSet) closeAll(log *slog.Logger) {
	s.mu.Lock()
	s.closed = true
	lanes := slices.Clone(s.lanes)
	s.mu.Unlock()
	var closing sync.WaitGroup
	for _, l := range lanes {
		closing.Go(func() {
			if cerr := l.close(); cerr != nil {
				log.Warn("listener close failed", "listener", l.name, "error", cerr)
			}
		})
	}
	closing.Wait()
}

// tokenWatch is the list of caller_token files watchCallerTokens re-reads.
// It is a registry for the same reason laneSet is: a reload adds and
// removes lanes, and their token files with them.
type tokenWatch struct {
	mu    sync.Mutex
	files []*token.File
}

func (w *tokenWatch) add(f *token.File) {
	if f == nil {
		return
	}
	w.mu.Lock()
	w.files = append(w.files, f)
	w.mu.Unlock()
}

func (w *tokenWatch) remove(f *token.File) {
	if f == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for i, g := range w.files {
		if g == f {
			w.files = append(w.files[:i:i], w.files[i+1:]...)
			return
		}
	}
}

func (w *tokenWatch) all() []*token.File {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]*token.File, len(w.files))
	copy(out, w.files)
	return out
}

// laneRuntime builds and starts lanes. Run assembles one, starts every
// lane of the startup config through it, then hands it to the reloader for
// the lanes later documents add.
//
// The facts a server is built from beyond its lane (audit sink, review
// readers, the ICA runtime) come from the startup config: they are in the
// reload baseline, so a document that reaches start carries the same ones.
type laneRuntime struct {
	cfg  *Config
	sink audit.Sink
	tel  *telemetry
	otel *otelExporter
	log  *slog.Logger
	set  *laneSet
	// tokens is the caller_token watcher's list; a lane's file joins it in
	// launch and leaves it in stop.
	tokens *tokenWatch

	// ctx, wg and fail are the serve side. Run sets them before the first
	// start: the context every Serve runs under, the group shutdown joins,
	// and where a Serve error goes to stop the process.
	ctx  context.Context
	wg   *sync.WaitGroup
	fail func(error)
}

// build wires one lane (metrics, telemetry recorder, caller token file)
// and constructs its server without binding. It updates the lane in
// place; the reloader publishes it to /config afterwards.
func (rt *laneRuntime) build(ln *lane) (runningLane, error) {
	ln.metrics = rt.tel.laneMetrics(ln.cfg.Protocol)
	// One recorder per lane for the process's life: a reload swaps rules
	// into the server built here, which keeps its recorder.
	ln.telemetry = rt.otel.lane(ln.name, ln.cfg.Protocol)
	if err := openCallerToken(ln, rt.log); err != nil {
		return runningLane{}, err
	}
	cfg := rt.cfg
	rl := runningLane{name: ln.name, protocol: ln.cfg.Protocol, tokens: ln.callerTokens, stopped: new(atomic.Bool)}
	var err error
	switch {
	case isGRPCTransport(ln.cfg):
		var rules *liveRules
		rl.endpoint, rules, err = buildGRPCServer(*ln, cfg.Audit, rt.sink, cfg.reviewSettler(rt.log), rt.log)
		rl.rules = rules
	case isSSH(ln.cfg):
		var rules *liveRules
		rl.endpoint, rules, err = buildSSHServer(*ln, cfg.Audit, rt.sink, rt.log)
		rl.rules = rules
	case isICA(ln.cfg):
		var rules *liveRules
		rl.endpoint, rules, err = buildICAServer(*ln, icaRuntimeFor(cfg), cfg.Audit, rt.sink, rt.log)
		rl.rules = rules
	case isMCPGateway(ln.cfg):
		var (
			msrv  MCPGatewayServer
			rules *liveRules
		)
		msrv, rules, err = buildMCPGatewayServer(*ln, cfg.Audit, rt.sink, rt.log)
		rl.endpoint = msrv
		if err == nil {
			rl.rules = mcpLaneRules{liveRules: rules, srv: msrv}
		}
	default:
		rl.relay, err = buildServer(*ln, cfg.Audit, rt.sink, cfg.reviewStatusReader(), cfg.reviewSettler(rt.log), rt.log)
		if err == nil {
			rl.rules = relayRules{rl.relay}
		}
	}
	if err != nil {
		return runningLane{}, err
	}
	return rl, nil
}

// launch registers the lane and runs its Serve. A relay lane binds here, so
// its bind error returns; an endpoint lane binds inside Serve. On an error
// nothing runs: the lane is closed and its token file is not watched. The
// lane serves under a context of its own, which stop cancels.
func (rt *laneRuntime) launch(rl runningLane) (<-chan error, error) {
	ctx, cancel := context.WithCancel(rt.ctx)
	rl.cancel = cancel
	rt.set.mu.Lock()
	var err error
	switch {
	case rt.set.closed:
		err = errors.New("the process is shutting down")
	case rl.relay != nil:
		err = rl.relay.Listen()
	}
	if err != nil {
		rt.set.mu.Unlock()
		cancel()
		_ = rl.close()
		return nil, err
	}
	rt.set.lanes = append(rt.set.lanes, rl)
	rt.wg.Add(1)
	rt.set.mu.Unlock()
	rt.tokens.add(rl.tokens)

	// served is buffered: a Serve that returns after its reader gave up
	// blocks nothing.
	served := make(chan error, 1)
	go func() {
		defer rt.wg.Done()
		served <- rl.serve(ctx)
	}()
	return served, nil
}

// watch stops the process through fail when a lane's Serve fails. A Serve
// that returns because stop closed the lane is not a failure.
func (rt *laneRuntime) watch(rl runningLane, served <-chan error) {
	if err := <-served; err != nil && !rl.stopped.Load() {
		rt.log.Error("listener failed", "listener", rl.name, "error", err)
		rt.fail(err)
	}
}

// start serves a startup lane. A relay lane's bind error returns. An
// endpoint lane binds inside Serve, and an ica lane loads its runtime for
// minutes before it does, so start does not wait for that bind: /readyz
// reports the lane until it is ready, and a Serve error stops the process
// through fail.
func (rt *laneRuntime) start(rl runningLane) error {
	served, err := rt.launch(rl)
	if err != nil {
		return err
	}
	go rt.watch(rl, served)
	return nil
}

// startAll starts the startup lanes in config order, then calls started.
// On the first lane that cannot start it reports the error through fail, so
// Run's one shutdown path runs, closes the lanes not started yet (they hold
// no socket; close releases an engine among them) and never calls started:
// the lanes that did start are not the config, and /readyz must not read
// them as ready.
func (rt *laneRuntime) startAll(built []runningLane, started func()) {
	for i, b := range built {
		if err := rt.start(b); err != nil {
			rt.log.Error("listener failed", "listener", b.name, "error", err)
			rt.fail(err)
			for _, rest := range built[i+1:] {
				_ = rest.close()
			}
			return
		}
	}
	started()
}

// startBound serves a lane a reload added. It returns once the socket is
// bound, or with the bind error and nothing running: the reload path
// commits a document only after each added lane has returned from it. A
// failure after the bind (the accept loop, an engine) stops the process
// through fail, the same as a startup lane's.
func (rt *laneRuntime) startBound(rl runningLane) error {
	served, err := rt.launch(rl)
	if err != nil {
		return err
	}
	if rl.relay != nil {
		go rt.watch(rl, served)
		return nil
	}
	// An endpoint lane publishes the address once bound, so startBound
	// polls for it. served has one reader at a time: this function until
	// the bind is decided, then watch. A bind failure therefore returns
	// to the reloader instead of reaching fail.
	deadline := time.NewTimer(bindWait)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for rl.endpoint.Addr() == nil {
		select {
		case err := <-served:
			rt.stop(rl.name)
			if err == nil {
				err = errors.New("the server stopped before binding")
			}
			return err
		case <-tick.C:
		case <-deadline.C:
			// stop closes the lane and cancels its context. The
			// reload does not wait for a Serve that ignores both.
			rt.stop(rl.name)
			return fmt.Errorf("%s: %w within %s", rl.name, errNoBind, bindWait)
		}
	}
	go rt.watch(rl, served)
	return nil
}

// stop closes a running lane and drops it from the registry: the socket
// closes, its connections close, and the next /stats does not list it.
// False when no lane of that name runs.
func (rt *laneRuntime) stop(name string) bool {
	rl, ok := rt.tel.retireLane(rt.set, name)
	if !ok {
		return false
	}
	rl.stopped.Store(true)
	rt.tokens.remove(rl.tokens)
	if err := rl.close(); err != nil {
		rt.log.Warn("listener close failed", "listener", rl.name, "error", err)
	}
	rl.cancel()
	return true
}
