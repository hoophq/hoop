package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/analytics"
	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/license"
	"github.com/hoophq/hoop/sidecar/proxy"
)

// earlyAddr is an endpoint whose Addr answers whatever its Ready says, as an
// engine's does once it closed. Only its Ready may say it serves.
type earlyAddr struct {
	endpointServer
	ready atomic.Bool
}

func (e *earlyAddr) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9} }
func (e *earlyAddr) Ready() bool    { return e.ready.Load() }

// A lane with Ready is judged by Ready, never by an address it reports
// before binding.
func TestReadyzJudgesALaneWithReadyByReadyAlone(t *testing.T) {
	lane := &earlyAddr{}
	h := servingHealth(endpointLane("tools", "mcp", lane))
	if code, r, _ := readyz(t, h); code != http.StatusServiceUnavailable || r.Lanes[0].State != laneNotReady {
		t.Errorf("before the bind: /readyz = %d %+v, want 503 not_ready", code, r.Lanes)
	}
	lane.ready.Store(true)
	if code, _, _ := readyz(t, h); code != http.StatusOK {
		t.Errorf("once bound: /readyz = %d, want 200", code)
	}
	lane.ready.Store(false)
	if code, r, _ := readyz(t, h); code != http.StatusServiceUnavailable || r.Lanes[0].State != laneNotReady {
		t.Errorf("after the close: /readyz = %d %+v, want 503", code, r.Lanes)
	}
}

func newRelay(t *testing.T, listen string) *proxy.Server {
	t.Helper()
	srv, err := proxy.NewServer(proxy.Config{
		Listen: listen, Upstream: "127.0.0.1:1", Protocol: inspect.Postgres, Connection: "appdb",
		Audit: audit.NewMemorySink(8), Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

// A real relay lane: starting before its bind, open while it accepts, closed
// once it stopped, and the process status follows. /healthz stays 200.
func TestReadyzFollowsARealRelay(t *testing.T) {
	relay := newRelay(t, "127.0.0.1:0")
	h := servingHealth(runningLane{name: "appdb", protocol: "postgres", relay: relay})

	if code, r, _ := readyz(t, h); code != http.StatusServiceUnavailable || r.Lanes[0].State != laneStarting {
		t.Errorf("before the bind: %d %+v, want 503 starting", code, r.Lanes)
	}
	done := make(chan error, 1)
	go func() { done <- relay.Serve(context.Background()) }()
	waitFor(t, func() bool { code, _, _ := readyz(t, h); return code == http.StatusOK })
	if _, r, _ := readyz(t, h); r.Lanes[0].State != laneOpen || !r.LanesOpen {
		t.Errorf("bound: %+v, want open", r)
	}

	_ = relay.Close()
	<-done
	code, r, _ := readyz(t, h)
	if code != http.StatusServiceUnavailable || r.Lanes[0].State != laneClosed || r.Reason != readyLanesNotOpen {
		t.Errorf("closed: %d %+v, want 503 lanes_not_open, closed", code, r)
	}
	if code := healthz(t, h); code != http.StatusOK {
		t.Errorf("/healthz after the lane closed = %d, want 200", code)
	}
}

// Run's shutdown (laneSet.closeAll) makes /readyz 503 before the first lane
// closes, also for a lane that keeps its address after Close (libhoop's
// gRPC and SSH).
func TestReadyzSaysStoppingOnceRunStops(t *testing.T) {
	h := servingHealth(endpointLane("orders", "grpc", boundLane{addr: &net.TCPAddr{Port: 1}}))
	if code, _, _ := readyz(t, h); code != http.StatusOK {
		t.Fatalf("serving: /readyz = %d, want 200", code)
	}
	h.lanes.mu.Lock()
	h.lanes.closed = true // what closeAll sets before it closes the lanes
	h.lanes.mu.Unlock()
	code, r, _ := readyz(t, h)
	if code != http.StatusServiceUnavailable || r.Reason != readyStopping || r.LanesOpen || r.Lanes[0].State != laneClosed {
		t.Errorf("stopping: %d %+v, want 503 stopping, closed", code, r)
	}
	if code := healthz(t, h); code != http.StatusOK {
		t.Errorf("/healthz while stopping = %d, want 200", code)
	}
}

// The real heartbeat, through a success, a 412 the process cannot import, an
// outage and a recovery. The body follows every answer; the status code
// never leaves 200, because the config in force stays.
func TestReadyzFollowsTheHeartbeatWithoutLeavingReady(t *testing.T) {
	rl, _ := testReloader(t, reloadBase)
	var phase atomic.Value
	phase.Store("ok")
	var calls atomic.Int32
	plane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		switch phase.Load() {
		case "no_config":
			w.WriteHeader(http.StatusPreconditionFailed)
		case "outage":
			w.WriteHeader(http.StatusServiceUnavailable)
		case "unreachable":
			if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
				_ = conn.Close()
			}
		default:
			w.Header().Set(LicenseManagedHeader, "true")
			_, _ = w.Write([]byte(reloadBase))
		}
	}))
	t.Cleanup(plane.Close)

	h := servingHealth()
	h.planeConfigured()
	h.planeAnswered(time.Now())
	cp := &controlPlane{url: plane.URL, cred: tokenCredential("hsc_x"), every: 2 * time.Millisecond,
		revision: "rev-1", outcome: reloadApplied.String(), health: h}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); cp.heartbeat(ctx, slog.New(slog.DiscardHandler), rl) }()
	t.Cleanup(func() { cancel(); <-done })

	for _, step := range []struct {
		phase     string
		connected bool
		code      string
	}{
		{"ok", true, ""},
		{"no_config", false, planeErrNoConfig},
		{"outage", false, planeErrServer},
		{"unreachable", false, planeErrUnreachable},
		{"ok", true, ""},
	} {
		phase.Store(step.phase)
		before := calls.Load()
		var r readiness
		waitFor(t, func() bool {
			// Two answers after the switch: the first can be in flight
			// under the previous phase.
			if calls.Load() < before+2 {
				return false
			}
			code, got, _ := readyz(t, h)
			if code != http.StatusOK {
				t.Fatalf("%s: /readyz = %d, want 200 whatever the plane answers", step.phase, code)
			}
			r = got
			return r.ConnectedToControlPlane == step.connected && r.ControlPlane.Error == step.code
		})
		if r.ControlPlane.LastHandshake == "" || !r.ConfigLoaded || r.Status != "ready" {
			t.Errorf("%s: %+v, want ready with the last good handshake", step.phase, r)
		}
	}
}

// /readyz is read from other goroutines while the boot fails, the lanes are
// published, a relay binds and closes, and the heartbeat reports. Run it
// under -race.
func TestReadyzUnderConcurrentUpdates(t *testing.T) {
	h := newHealthStatus()
	h.planeConfigured()
	relay := newRelay(t, "127.0.0.1:0")
	mux := http.NewServeMux()
	handleHealth(mux, h)

	stop := make(chan struct{})
	var readers sync.WaitGroup
	// The readers stop on every way out of the test, a failed assertion
	// included.
	t.Cleanup(func() {
		select {
		case <-stop:
		default:
			close(stop)
		}
		readers.Wait()
	})
	for range 4 {
		readers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
				var r readiness
				if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
					t.Errorf("a torn /readyz body: %v", err)
					return
				}
			}
		})
	}
	for i := range 50 {
		h.bootFailed(planeUnavailableError{status: 503, err: fmt.Errorf("attempt %d", i)})
	}
	h.planeAnswered(time.Now())
	set := &laneSet{}
	h.serving(set, false, func() license.Status { return license.Status{} })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	set.mu.Lock()
	set.lanes = append(set.lanes, runningLane{name: "appdb", protocol: "postgres", relay: relay})
	set.mu.Unlock()
	served := make(chan error, 1)
	go func() { served <- relay.Serve(ctx) }()
	for i := range 50 {
		// A reload adding and removing a listener while the probes read.
		if i%2 == 0 {
			h.planeFailed(planeUnavailableError{err: errors.New("down")})
			set.mu.Lock()
			set.lanes = append(set.lanes, endpointLane("orders", "grpc", boundLane{addr: &net.TCPAddr{Port: 1}}))
			set.mu.Unlock()
		} else {
			h.planeAnswered(time.Now())
			set.remove("orders")
		}
	}
	// Close a lane that serves, so the readers see it open, then closed.
	waitFor(t, relay.Serving)
	set.closeAll(slog.New(slog.DiscardHandler))
	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after Close")
	}
}

// mainArgs runs Main with args, as the binary would.
func mainArgs(t *testing.T, args ...string) error {
	t.Helper()
	prevArgs, prevVersion := os.Args, Version
	t.Cleanup(func() { os.Args, Version = prevArgs, prevVersion })
	os.Args = append([]string{"hoop-inspect"}, args...)
	return Main("test", nil, nil)
}

func portFree(t *testing.T, addr string) {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("the health port %s is still held: %v", addr, err)
	}
	_ = ln.Close()
}

// A check inherits HOOP_SIDECAR_HEALTH_LISTEN inside a pod whose sidecar
// holds the port. It must never try to bind it.
func TestChecksNeverBindTheHealthPort(t *testing.T) {
	t.Setenv(analytics.EnvVar, "off")
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	t.Setenv(HealthListenEnv, held.Addr().String())
	path := writeConfig(t, minimalConfig+`}`)

	if err := mainArgs(t, "-validate", "-config", path); err != nil {
		t.Errorf("-validate with the health port held = %v, want nil", err)
	}
	// No grpc lane named "nope": an error, and not the health port's.
	if err := mainArgs(t, "-grpc-discover", "nope", "-config", path); err == nil || strings.Contains(err.Error(), HealthListenEnv) {
		t.Errorf("-grpc-discover with the health port held = %v, want its own error", err)
	}
	// The CLI's Connect page and setup screen call SetupWith with no
	// listener: Setup binds nothing.
	srv, _ := scriptedPlane(t)
	t.Setenv(ControlPlaneURLEnv, srv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_x")
	if _, _, err := SetupWith("", nil, nil, WithoutBootRetry()); err != nil {
		t.Errorf("SetupWith with the health port held = %v, want nil", err)
	}
}

// The early listener is released on every way out of the boot: a Setup that
// fails, a Run that fails before it serves, and a normal shutdown.
func TestTheHealthPortIsReleasedOnEveryExit(t *testing.T) {
	t.Setenv(analytics.EnvVar, "off")
	fastBootRetry(t, time.Minute)

	t.Run("setup fails", func(t *testing.T) {
		addr := freeTCPAddr(t)
		t.Setenv(HealthListenEnv, addr)
		srv, _ := scriptedPlane(t, http.StatusUnauthorized)
		t.Setenv(ControlPlaneURLEnv, srv.URL)
		t.Setenv(SidecarTokenEnv, "hsc_x")
		if err := mainArgs(t); err == nil || !strings.Contains(err.Error(), "rejected the token") {
			t.Fatalf("Main = %v, want the refused token", err)
		}
		portFree(t, addr)
	})

	t.Run("run fails before it serves", func(t *testing.T) {
		addr := freeTCPAddr(t)
		t.Setenv(HealthListenEnv, addr)
		t.Setenv(ControlPlaneURLEnv, "")
		t.Setenv(SidecarTokenEnv, "")
		// The lane binds the health port: Run refuses the config before
		// any lane is published.
		path := writeConfig(t, `{"listeners":[{"protocol":"postgres","listen":"`+addr+`","upstream":"h:5432"}]}`)
		if err := mainArgs(t, "-config", path); err == nil || !strings.Contains(err.Error(), HealthListenEnv) {
			t.Fatalf("Main = %v, want the port conflict", err)
		}
		portFree(t, addr)
	})

	t.Run("normal shutdown", func(t *testing.T) {
		addr := freeTCPAddr(t)
		t.Setenv(HealthListenEnv, addr)
		t.Setenv(ControlPlaneURLEnv, "")
		t.Setenv(SidecarTokenEnv, "")
		path := writeConfig(t, `{"listeners":[{"protocol":"postgres","listen":"`+freeTCPAddr(t)+
			`","upstream":"h:5432"}],"audit":{"file":"`+t.TempDir()+`/audit.jsonl"}}`)
		done := make(chan error, 1)
		go func() { done <- mainArgs(t, "-config", path) }()
		// Ready means Run published its lanes, after it took over SIGTERM.
		waitFor(t, func() bool {
			code, _ := httpCall(t, http.MethodGet, "http://"+addr+"/readyz")
			return code == http.StatusOK
		})
		if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Main after SIGTERM = %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("Main did not stop on SIGTERM")
		}
		portFree(t, addr)
	})
}

// ENG-1116 after ENG-1127: a reload starts and stops listeners in place, and
// /readyz follows the running set, never the lanes Run started with. Here
// one document removes "second" and adds "third": while it applies, every
// probe names only lanes that run; after it, the removed lane is gone and
// the added one is open. (A relay lane binds before it joins the set; an
// endpoint lane joins first and reads starting until its bind.)
func TestReadyzFollowsAReloadThatAddsAndRemovesListeners(t *testing.T) {
	base := withUpstream(t, reloadBase)
	listener := func(name string) string {
		return fmt.Sprintf(`{"name": %q, "protocol": "postgres", "listen": %q, "upstream": "h2:5432"}, `, name, freeTCPAddr(t))
	}
	before := editJSON(t, base, `"listeners": [{`, `"listeners": [`+listener("second")+`{`)
	after := editJSON(t, base, `"listeners": [{`, `"listeners": [`+listener("third")+`{`)
	rl, buf, rt := testRunningReloader(t, before)
	h := newHealthStatus()
	h.serving(rt.set, false, func() license.Status { return license.Status{} })

	names := func(r readiness) map[string]string {
		out := map[string]string{}
		for _, l := range r.Lanes {
			out[l.Name] = l.State
		}
		return out
	}
	waitFor(t, func() bool { code, _, _ := readyz(t, h); return code == http.StatusOK })
	if _, r, _ := readyz(t, h); fmt.Sprint(names(r)) != "map[appdb:open second:open]" {
		t.Fatalf("before the reload: lanes = %v", names(r))
	}

	// Probe through the reload from another goroutine.
	stop := make(chan struct{})
	var seen sync.Map
	var notReady atomic.Int32
	var prober sync.WaitGroup
	prober.Go(func() {
		mux := http.NewServeMux()
		handleHealth(mux, h)
		for {
			select {
			case <-stop:
				return
			default:
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			var r readiness
			if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
				t.Errorf("a torn /readyz body: %v", err)
				return
			}
			if w.Code != http.StatusOK {
				notReady.Add(1)
			}
			for name, state := range names(r) {
				seen.Store(name+"="+state, true)
			}
		}
	})
	got := handleWith(rl, buf, after)
	close(stop)
	prober.Wait()
	if got != reloadApplied {
		t.Fatalf("outcome = %v, want applied; log:\n%s", got, buf)
	}
	// Both lanes here are relays, bound before they join the set: no probe
	// during the reload may leave 200.
	if n := notReady.Load(); n != 0 {
		t.Errorf("%d probes during the reload answered 503", n)
	}
	// The answers a probe may give while the reload runs. A probe that
	// snapshots the set before "second" is removed reads it open or skips
	// it once its stop began: never closed, so a removal never turns
	// /readyz to 503. The added relay binds before it joins the set; the
	// starting state is allowed for an engine that would join first.
	seen.Range(func(k, _ any) bool {
		switch k.(string) {
		case "appdb=open", "second=open", "third=open", "third=starting":
		default:
			t.Errorf("a probe during the reload read %s", k)
		}
		return true
	})

	code, r, _ := readyz(t, h)
	if code != http.StatusOK || fmt.Sprint(names(r)) != "map[appdb:open third:open]" || !r.LanesOpen {
		t.Errorf("after the reload: %d %v, want 200 with appdb and third open and no second", code, names(r))
	}
}

// A startup lane that cannot bind stops the process. Until it is gone, the
// early health listener must never answer 200: the lanes that did start are
// not the config, whether the failure is the first lane's or a later one's.
func TestReadyzNeverReadsReadyWhenAStartupLaneFails(t *testing.T) {
	t.Setenv(analytics.EnvVar, "off")
	t.Setenv(ControlPlaneURLEnv, "")
	t.Setenv(SidecarTokenEnv, "")
	for _, tc := range []struct {
		name     string
		failLane int
	}{
		{"the first lane", 0},
		{"a later lane", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			held, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			listens := []string{freeTCPAddr(t), freeTCPAddr(t)}
			listens[tc.failLane] = held.Addr().String()
			addr := freeTCPAddr(t)
			t.Setenv(HealthListenEnv, addr)
			path := writeConfig(t, `{"listeners":[`+
				`{"name":"first","protocol":"postgres","listen":"`+listens[0]+`","upstream":"h:5432"},`+
				`{"name":"second","protocol":"postgres","listen":"`+listens[1]+`","upstream":"h:5432"}],`+
				`"audit":{"file":"`+t.TempDir()+`/audit.jsonl"}}`)

			stop := make(chan struct{})
			var ready atomic.Int32
			var probes atomic.Int32
			var prober sync.WaitGroup
			prober.Go(func() {
				client := &http.Client{Timeout: time.Second}
				for {
					select {
					case <-stop:
						return
					default:
					}
					resp, err := client.Get("http://" + addr + "/readyz")
					if err != nil {
						continue // not bound yet, or already closed
					}
					_ = resp.Body.Close()
					probes.Add(1)
					if resp.StatusCode == http.StatusOK {
						ready.Add(1)
					}
				}
			})
			err = mainArgs(t, "-config", path)
			close(stop)
			prober.Wait()
			if err == nil || !strings.Contains(err.Error(), held.Addr().String()) {
				t.Fatalf("Main = %v, want the bind failure on %s", err, held.Addr())
			}
			if n := ready.Load(); n != 0 {
				t.Errorf("/readyz answered 200 %d times while a startup lane had failed", n)
			}
			t.Logf("%d probes answered", probes.Load())
		})
	}
}

// startAll publishes the startup lanes to the probes only when every one of
// them started. A failure, of the first lane or a later one, reaches fail and
// never publishes: the lanes that did start would read as ready.
func TestStartAllPublishesOnlyAFullStart(t *testing.T) {
	for _, tc := range []struct {
		name     string
		failLane int // -1: none fails
	}{
		{"all start", -1},
		{"the first lane fails", 0},
		{"a later lane fails", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			held, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			ctx, cancel := context.WithCancel(context.Background())
			var wg sync.WaitGroup
			var failed []error
			log := slog.New(slog.DiscardHandler)
			rt := &laneRuntime{log: log, set: &laneSet{}, tokens: &tokenWatch{},
				ctx: ctx, wg: &wg, fail: func(err error) { failed = append(failed, err) }}
			t.Cleanup(func() { cancel(); rt.set.closeAll(log); wg.Wait() })
			var built []runningLane
			for i, name := range []string{"first", "second", "third"} {
				listen := "127.0.0.1:0"
				if i == tc.failLane {
					listen = held.Addr().String()
				}
				built = append(built, runningLane{name: name, protocol: "postgres",
					relay: newRelay(t, listen), stopped: new(atomic.Bool)})
			}

			h := newHealthStatus()
			rt.startAll(built, func() { h.serving(rt.set, false, func() license.Status { return license.Status{} }) })
			code, r, _ := readyz(t, h)
			if tc.failLane < 0 {
				if len(failed) != 0 || code != http.StatusOK || len(r.Lanes) != 3 {
					t.Fatalf("a full start: fail %v, /readyz %d %+v; want published and ready", failed, code, r)
				}
				return
			}
			if len(failed) != 1 || !strings.Contains(failed[0].Error(), held.Addr().String()) {
				t.Errorf("fail got %v, want the one bind error", failed)
			}
			if code != http.StatusServiceUnavailable || r.ConfigLoaded || r.Status == "ready" {
				t.Errorf("after a failed start: /readyz %d %+v, want 503 and nothing published", code, r)
			}
			// The lanes after the failed one never bound.
			for _, b := range built[tc.failLane+1:] {
				if b.relay.Addr() != nil {
					t.Errorf("lane %s bound after an earlier lane failed", b.name)
				}
			}
		})
	}
}
