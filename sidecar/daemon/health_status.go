package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/hoophq/hoop/sidecar/license"
	"github.com/hoophq/hoop/sidecar/proxy"
)

// healthStatus is what /readyz reports. One value per process: SetupWith
// creates it so the boot handshake reports into it, Run publishes the lanes,
// and the heartbeat reports every later handshake. Every listener that
// serves the probes reads the same value, so a probe gets the same answer
// from any of them.
//
// It holds booleans, counts, times and reason codes, never an error's text:
// the probes have no authentication, and an error can name the plane's URL,
// an upstream or a credential source.
type healthStatus struct {
	pid int

	mu sync.Mutex
	// plane* describe the control plane connection; planeEnabled is false
	// for a process that runs from its file alone.
	planeEnabled   bool
	planeConnected bool
	planeLast      time.Time
	planeError     string
	bootAttempts   int
	// lanes is the registry Run serves from, nil until Run started the
	// startup lanes: until then no config is loaded. It is read on every
	// call, so a lane a reload adds or removes shows on the next probe.
	lanes   *laneSet
	license func() license.Status
	// icaReadinessJSON is admin.ica_readiness_json: /readyz answers with the
	// ICA inspection attestation instead of the process status.
	icaReadinessJSON bool
}

func newHealthStatus() *healthStatus { return &healthStatus{pid: os.Getpid()} }

// The reason codes /readyz and its control plane block use. A code is never
// removed or renamed: scripts compare against it.
const (
	readyWaitingForPlane = "waiting_for_control_plane"
	readyStarting        = "starting"
	readyLanesNotOpen    = "lanes_not_open"
	readyStopping        = "stopping"

	planeErrUnreachable = "unreachable"
	planeErrTimeout     = "timeout"
	planeErrServer      = "server_error"
	planeErrNoConfig    = "no_config"
	planeErrRefused     = "refused"
)

// planeErrorCode names a failed handshake for the probes, without its text.
func planeErrorCode(err error) string {
	var unavailable planeUnavailableError
	switch {
	case errors.Is(err, ErrPlaneHasNoConfig):
		return planeErrNoConfig
	case errors.As(err, &unavailable) && unavailable.status >= 500:
		return planeErrServer
	case errors.Is(err, context.DeadlineExceeded) || isTimeout(err):
		return planeErrTimeout
	case errors.As(err, &unavailable):
		return planeErrUnreachable
	}
	return planeErrRefused
}

func isTimeout(err error) bool {
	var t interface{ Timeout() bool }
	return errors.As(err, &t) && t.Timeout()
}

// The methods below accept a nil receiver, so a caller with no status
// reports into nothing.

// planeConfigured records that this process gets its config from a plane.
func (h *healthStatus) planeConfigured() {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.planeEnabled = true
	h.mu.Unlock()
}

// bootFailed records one failed boot request.
func (h *healthStatus) bootFailed(err error) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.bootAttempts++
	h.planeConnected, h.planeError = false, planeErrorCode(err)
	h.mu.Unlock()
}

// planeAnswered records a handshake the plane answered with a document.
func (h *healthStatus) planeAnswered(at time.Time) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.planeConnected, h.planeLast, h.planeError = true, at, ""
	h.mu.Unlock()
}

// planeFailed records a failed heartbeat. The config in force stays, and so
// does the readiness it earned: only the body says the plane is lost.
func (h *healthStatus) planeFailed(err error) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.planeConnected, h.planeError = false, planeErrorCode(err)
	h.mu.Unlock()
}

// serving publishes the registry Run serves from, once its startup lanes
// are in it, and where the license in force is read from. The config is
// loaded from here on. The process stops when the registry closes
// (laneSet.closeAll).
func (h *healthStatus) serving(lanes *laneSet, icaReadinessJSON bool, lic func() license.Status) {
	h.mu.Lock()
	h.lanes, h.icaReadinessJSON, h.license = lanes, icaReadinessJSON, lic
	h.mu.Unlock()
}

// readiness is the /readyz body. A field is never removed or renamed:
// scripts read it.
type readiness struct {
	Status string `json:"status"`
	// Reason is set when Status is not_ready: a code, never a message.
	Reason                  string          `json:"reason,omitempty"`
	ProcessRunning          bool            `json:"process_running"`
	PID                     int             `json:"pid"`
	ValidLicense            bool            `json:"valid_license"`
	ConfigLoaded            bool            `json:"config_loaded"`
	LanesOpen               bool            `json:"lanes_open"`
	ControlPlaneEnabled     bool            `json:"control_plane_enabled"`
	ConnectedToControlPlane bool            `json:"connected_to_control_plane"`
	ControlPlane            *planeReadiness `json:"control_plane,omitempty"`
	Lanes                   []laneReadiness `json:"lanes"`
}

type planeReadiness struct {
	// LastHandshake is the last handshake the plane answered, RFC 3339 UTC.
	LastHandshake string `json:"last_handshake,omitempty"`
	// Error is the reason code of the last failed handshake, empty after a
	// success.
	Error string `json:"error,omitempty"`
	// BootAttempts counts the failed requests of the boot.
	BootAttempts int `json:"boot_attempts,omitempty"`
}

type laneReadiness struct {
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	// State is open, starting (not bound yet), not_ready (the lane says it
	// cannot serve: an ICA engine loading or draining, a gRPC lane still
	// without its schema) or closed (it stopped accepting).
	State string `json:"state"`
}

const (
	laneOpen     = "open"
	laneStarting = "starting"
	laneNotReady = "not_ready"
	laneClosed   = "closed"
)

// report builds the /readyz body.
//
// Ready means a config is loaded and every lane serves. A control plane lost
// after the config loaded never makes the process not ready: the lanes keep
// serving the last good config, and taking the pod out of rotation would
// turn a lost phone line home into an outage. Only the body says so.
func (h *healthStatus) report() readiness {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := readiness{
		Status:                  "ready",
		ProcessRunning:          true,
		PID:                     h.pid,
		ControlPlaneEnabled:     h.planeEnabled,
		ConnectedToControlPlane: h.planeConnected,
		ConfigLoaded:            h.lanes != nil,
		Lanes:                   []laneReadiness{},
	}
	if h.planeEnabled {
		p := &planeReadiness{Error: h.planeError, BootAttempts: h.bootAttempts}
		if !h.planeLast.IsZero() {
			p.LastHandshake = h.planeLast.UTC().Format(time.RFC3339)
		}
		r.ControlPlane = p
	}
	if h.license != nil {
		r.ValidLicense = h.license().State() == license.StateValid
	}
	if h.lanes == nil {
		// No config yet: the one case a control plane makes the process
		// not ready. Once it answered, what is left is local work (the
		// detector, the plug-ins, the lanes).
		r.Status, r.Reason = "not_ready", readyStarting
		if h.planeEnabled && h.planeLast.IsZero() {
			r.Reason = readyWaitingForPlane
		}
		return r
	}
	r.LanesOpen = true
	add := func(name, protocol, state string) {
		r.Lanes = append(r.Lanes, laneReadiness{Name: name, Protocol: protocol, State: state})
		r.LanesOpen = r.LanesOpen && state == laneOpen
	}
	stopped := h.lanes.isClosed()
	for _, l := range h.lanes.snapshot() {
		var state string
		if l.relay != nil {
			state = relayState(l.relay)
		} else {
			state = endpointState(l.endpoint, stopped)
		}
		// A reload removing this lane took it out of the set after this
		// snapshot. Its state is read before this check, and stop marks
		// the lane before it closes it: a lane read as closed here is
		// therefore always skipped, and a probe that races a removal
		// reads the lane open or not at all, never as a failure.
		if l.stopped != nil && l.stopped.Load() {
			continue
		}
		add(l.name, l.protocol, state)
	}
	switch {
	case stopped:
		r.Status, r.Reason, r.LanesOpen = "not_ready", readyStopping, false
	case !r.LanesOpen:
		r.Status, r.Reason = "not_ready", readyLanesNotOpen
	}
	return r
}

// relayState reads a relay lane: open from its bind until it closes.
func relayState(s *proxy.Server) string {
	switch {
	case s.Serving():
		return laneOpen
	case s.Addr() == nil:
		return laneStarting
	}
	return laneClosed
}

// endpointState reads a lane that terminates its protocol. A lane that
// reports readiness is judged by it alone: the MCP lane's Ready covers its
// bind and its close, an ICA lane in slot mode has no one bound address.
// The libhoop endpoints (static gRPC, SSH) report none: they are open once
// bound, until Run stops, because their Addr stays set after Close.
func endpointState(s endpointServer, stopped bool) string {
	if r, ok := s.(interface{ Ready() bool }); ok {
		if r.Ready() {
			return laneOpen
		}
		return laneNotReady
	}
	switch {
	case s.Addr() == nil:
		return laneStarting
	case stopped:
		return laneClosed
	}
	return laneOpen
}

// icaLanes returns the registry when it asks for the ICA attestation.
func (h *healthStatus) icaLanes() *laneSet {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.lanes != nil && h.icaReadinessJSON {
		return h.lanes
	}
	return nil
}

// handleHealth mounts /healthz and /readyz on mux.
func handleHealth(mux *http.ServeMux, h *healthStatus) {
	// The process answers, so it is alive, also while the boot retries
	// its handshake: a restart would only start that retry again.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	// Probes read the status code; the JSON is for the person who reads
	// it. An ica lane's engine loads a model and starts OCR workers after
	// the socket binds, which can take minutes, and refuses connections
	// until then: that lane reports not_ready meanwhile.
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if lanes := h.icaLanes(); lanes != nil {
			// admin.ica_readiness_json: the attestation ICA deployments
			// parse, unchanged.
			endpoints, _ := lanes.endpoints()
			writeICAReadiness(w, endpoints, lanes.relayCount())
			return
		}
		rep := h.report()
		w.Header().Set("Content-Type", "application/json")
		if rep.Status != "ready" {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(rep)
	})
}

func newHealthServer(h *healthStatus) *http.Server {
	mux := http.NewServeMux()
	handleHealth(mux, h)
	return &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
}

// serveHealth binds addr and serves the health routes until ctx ends. A bind
// failure is returned: the platform probes this address, and a process it
// cannot probe gets restarted in a loop.
func serveHealth(ctx context.Context, addr string, h *healthStatus, log *slog.Logger) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("health listener (%s): %w", HealthListenEnv, err)
	}
	srv := newHealthServer(h)
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	log.Info("health endpoint listening", "listen", ln.Addr().String())
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("health listener: %w", err)
	}
	return nil
}

// HealthListener serves /healthz and /readyz on HealthListenEnv from the
// start of the process, before the config is known. The boot can retry its
// control plane handshake for minutes; a platform that probes this address
// meanwhile reads a live process that is not ready yet, not a refused
// connection.
//
// Only the run path binds it: a check (-validate, -grpc-discover) inherits the
// same environment inside a pod whose sidecar already holds the port. Pass
// it to SetupWith with WithHealthListener; Run then serves the lanes
// through it and closes it when it returns.
type HealthListener struct {
	status *healthStatus
	srv    *http.Server
	// ln is the bound socket. Close closes it itself: Shutdown closes only
	// the listeners Serve has registered, and Serve runs in a goroutine
	// that may not have reached that yet.
	ln   net.Listener
	addr string
	once sync.Once
}

// ListenHealth binds HealthListenEnv and serves the probes until Close. It
// returns nil, nil when the variable is unset. The check against the ports
// the config binds runs later, in Run, once the config is known.
func ListenHealth() (*HealthListener, error) {
	addr := strings.TrimSpace(os.Getenv(HealthListenEnv))
	if addr == "" {
		return nil, nil
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return nil, fmt.Errorf("%s %q: want host:port: %w", HealthListenEnv, addr, err)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("health listener (%s): %w", HealthListenEnv, err)
	}
	l := &HealthListener{status: newHealthStatus(), ln: ln, addr: ln.Addr().String()}
	l.srv = newHealthServer(l.status)
	go func() { _ = l.srv.Serve(ln) }()
	jsonLogger(os.Stderr, "").Info("health endpoint listening", "listen", l.addr)
	return l, nil
}

// Addr is the bound address.
func (l *HealthListener) Addr() string { return l.addr }

// Close stops the listener and releases its port before it returns, on
// every path. Safe on nil and to call twice.
func (l *HealthListener) Close() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = l.srv.Shutdown(ctx)
		// A Serve that had not registered ln yet left it open, and would
		// close it only when it returns, in its own goroutine. Close it
		// here: the port is free when Close returns. Serve then fails its
		// first Accept, or returns ErrServerClosed.
		_ = l.ln.Close()
	})
	return nil
}

// WithHealthListener hands SetupWith the listener ListenHealth bound, so the
// boot reports into what it serves and Run serves through it. Nil does
// nothing, so an entry point passes ListenHealth's result without branching.
func WithHealthListener(l *HealthListener) Option {
	return func(o *setupOptions) { o.health = l }
}
