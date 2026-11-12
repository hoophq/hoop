package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/identity/token"
	"github.com/hoophq/hoop/sidecar/proxy"
)

// This file is where a lane becomes a running server, and the one list of
// those servers. Run used to hold the list as parallel slices built once;
// a reload that adds or removes a listener (ADR-0014, amended) has to grow
// and shrink it while /stats, /readyz, the usage ticker and the shutdown
// loop read it, so the list is a registry under a mutex and the build and
// bind steps are methods the startup path and the reload path share. One
// build path: a lane started by a reload is the lane startup would have
// built from the same document.

// bindWait bounds how long start waits for an endpoint lane to publish its
// bound address. A bind is local and immediate; the wait only catches an
// engine that hangs before binding, so the reload does not hang with it.
const bindWait = 10 * time.Second

// runningLane is one lane as the process runs it: the server, how a reload
// swaps its rules, and the facts the stats zip and the usage event read.
type runningLane struct {
	name     string
	protocol string
	// relay is set for a relay lane (postgres, mysql, http, ...); endpoint
	// for one that terminates the protocol in-process (grpc, spanner, ssh,
	// ica, mcp). Exactly one is non-nil.
	relay    *proxy.Server
	endpoint endpointServer
	rules    ruleSwapper
	// tokens is the lane's caller_token file, nil without the block. Kept
	// so a removed lane leaves the token watcher's list with it.
	tokens *token.File
	// stopped is set by stop before Close, so the serve goroutine does not
	// report the Close as a listener failure.
	stopped *atomic.Bool
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

// laneSet is the registry of running lanes, in config order. Every reader
// takes a snapshot; add and remove are the reloader's, closeAll is Run's
// shutdown, after which add refuses: a lane the reloader starts during
// shutdown would outlive the WaitGroup that joins the others.
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

// closeAll closes every lane and refuses later adds. Endpoint lanes close
// together: an ica lane's Close drains its sessions for up to 30s, and
// closing lanes one after another would add those windows up past the
// pod's termination grace period.
func (s *laneSet) closeAll(log *slog.Logger) {
	s.mu.Lock()
	s.closed = true
	lanes := s.lanes
	s.lanes = nil
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
// A registry for the same reason laneSet is: a reload adds and removes
// lanes, and their token files with them.
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
	// build and leaves it in stop.
	tokens *tokenWatch

	// ctx, wg and fail are the serve side, set by Run before the first
	// start: the context every Serve runs under, the group shutdown joins,
	// and where a Serve error goes to stop the process.
	ctx  context.Context
	wg   *sync.WaitGroup
	fail func(error)
}

// build wires one lane the way Run wires every startup lane (metrics,
// telemetry recorder, caller token file) and constructs its server without
// binding. The lane is updated in place: the reloader publishes it to
// /config afterwards.
func (rt *laneRuntime) build(ln *lane) (runningLane, error) {
	ln.metrics = rt.tel.laneMetrics(ln.cfg.Protocol)
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
	rt.tokens.add(rl.tokens)
	return rl, nil
}

// start binds the lane and serves it. It returns once the socket is bound,
// or with the bind error and nothing running: the reload path commits a
// document only after every added lane answered this. A failure after the
// bind (the accept loop, an engine) stops the process through fail, the
// same as a startup lane's; a Serve that returns because stop closed the
// lane is not a failure.
func (rt *laneRuntime) start(rl runningLane) error {
	rt.set.mu.Lock()
	if rt.set.closed {
		rt.set.mu.Unlock()
		return errors.New("the process is shutting down")
	}
	if rl.relay != nil {
		if err := rl.relay.Listen(); err != nil {
			rt.set.mu.Unlock()
			return err
		}
	}
	rt.set.lanes = append(rt.set.lanes, rl)
	rt.wg.Add(1)
	rt.set.mu.Unlock()

	// served has exactly one reader: this function while the bind is
	// undecided, the watcher below once it is. That is what keeps a bind
	// failure on a reload-added lane from being reported as a process
	// failure.
	served := make(chan error, 1)
	go func() {
		defer rt.wg.Done()
		served <- rl.serve(rt.ctx)
	}()
	watch := func() {
		if err := <-served; err != nil && !rl.stopped.Load() {
			rt.log.Error("listener failed", "listener", rl.name, "error", err)
			rt.fail(err)
		}
	}
	if rl.relay != nil {
		go watch()
		return nil
	}
	// An endpoint lane binds inside Serve and publishes the address once
	// bound; that is the signal. The deadline only catches an engine that
	// never gets there.
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
			rt.stop(rl.name)
			<-served
			return fmt.Errorf("%s: did not bind within %s", rl.name, bindWait)
		}
	}
	go watch()
	return nil
}

// stop closes a running lane and drops it from the registry: the socket
// closes, its connections close, and the next /stats does not list it.
// False when no lane of that name runs.
func (rt *laneRuntime) stop(name string) bool {
	rl, ok := rt.set.remove(name)
	if !ok {
		return false
	}
	rl.stopped.Store(true)
	rt.tokens.remove(rl.tokens)
	if err := rl.close(); err != nil {
		rt.log.Warn("listener close failed", "listener", rl.name, "error", err)
	}
	return true
}
