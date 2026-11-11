package daemon

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// loadingLane is an endpoint whose readiness the test flips while the
// listener reads it from another goroutine.
type loadingLane struct {
	fakeICAServer
	ready atomic.Bool
}

func (l *loadingLane) Ready() bool { return l.ready.Load() }

// The health listener exists so the admin listener can be off. It must answer
// the probes and refuse every admin route, or turning admin off would hide
// nothing: the audit trail and the lane rules would move to this port.
func TestHealthListenerServesOnlyProbes(t *testing.T) {
	addr := freeTCPAddr(t)
	lane := &loadingLane{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serveHealth(ctx, addr, healthState{endpoints: []endpointServer{lane}, endpointNames: []string{"desktops"}}, slog.New(slog.DiscardHandler))
	}()

	base := "http://" + addr
	waitFor(t, func() bool {
		code, _ := httpCall(t, http.MethodGet, base+"/healthz")
		return code == http.StatusOK
	})

	if code, body := httpCall(t, http.MethodGet, base+"/readyz"); code != http.StatusServiceUnavailable || !strings.Contains(body, "desktops") {
		t.Errorf("/readyz while the lane loads = %d %q, want 503 naming desktops", code, body)
	}
	lane.ready.Store(true)
	if code, _ := httpCall(t, http.MethodGet, base+"/readyz"); code != http.StatusOK {
		t.Errorf("/readyz once the lane is ready = %d, want 200", code)
	}

	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/stats"},
		{http.MethodGet, "/metrics"},
		{http.MethodGet, "/config"},
		{http.MethodGet, "/events"},
		{http.MethodGet, "/api/sessions"},
		{http.MethodGet, "/api/events"},
		{http.MethodPut, "/license"},
		{http.MethodPost, "/v1/drain"},
	} {
		if code, _ := httpCall(t, route.method, base+route.path); code != http.StatusNotFound {
			t.Errorf("%s %s on the health listener = %d, want 404", route.method, route.path, code)
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serveHealth after shutdown = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serveHealth did not return after the context ended")
	}
}

// A probe on an address the process could not bind fails forever, so the
// bind error must stop the process instead of leaving it running unprobed.
func TestHealthListenerReturnsBindFailure(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()

	err = serveHealth(context.Background(), held.Addr().String(), healthState{}, slog.New(slog.DiscardHandler))
	if err == nil || !strings.Contains(err.Error(), HealthListenEnv) {
		t.Fatalf("serveHealth on a held port = %v, want an error naming %s", err, HealthListenEnv)
	}
}

// A health port that another listener of the config also binds is refused
// before anything binds, naming the other listener. An ICA slot range counts
// as bound on every port it spans, ports compare the way net.Listen resolves
// them, and two distinct local addresses may share a port.
func TestHealthListenRefusesPortsTheConfigBinds(t *testing.T) {
	cfg := &Config{
		Admin: AdminConfig{Listen: "0.0.0.0:19000"},
		MCP:   &MCPConfig{Listen: "127.0.0.1:8765"},
		Listeners: []ListenerConfig{
			{Name: "appdb", Protocol: "postgres", Listen: "0.0.0.0:15432"},
			{Name: "desktops", Protocol: "ica", Listen: "0.0.0.0:19001",
				ICA: &ICAConfig{Slots: &ICASlotsConfig{FirstPort: 19001, Count: 4}}},
			{Name: "web", Protocol: "http", Listen: "0.0.0.0:http"},
			{Name: "sock", Protocol: "postgres", Network: "unix", Listen: "/tmp/19006.sock"},
		},
	}
	for _, tc := range []struct {
		addr, want string
	}{
		{"0.0.0.0:19000", "admin listener"},
		{"0.0.0.0:019000", "admin listener"},
		{"0.0.0.0:8765", "MCP server"},
		{"127.0.0.1:8765", "MCP server"},
		{"127.0.0.1:15432", "appdb"},
		{"0.0.0.0:15432", "appdb"},
		{"0.0.0.0:19001", "desktops"},
		{"0.0.0.0:19003", "desktops"},
		{"0.0.0.0:19004", "desktops"},
		{"0.0.0.0:80", "web"},
		{"[::]:15432", "appdb"},
		{":15432", "appdb"},
		{"19001", "host:port"},
		{"0.0.0.0:no-such-service", "not a port number"},
	} {
		err := checkHealthListen(cfg, tc.addr)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("checkHealthListen(%q) = %v, want an error saying %q", tc.addr, err, tc.want)
		}
	}
	// [::1] is IPv6, and 0.0.0.0 leaves IPv6 free; 10.0.0.5 is a distinct
	// IPv4 address from the MCP server's 127.0.0.1.
	for _, addr := range []string{"", "0.0.0.0:19005", "0.0.0.0:19006", "127.0.0.1:0", "10.0.0.5:8765", "[::1]:19000"} {
		if err := checkHealthListen(cfg, addr); err != nil {
			t.Errorf("checkHealthListen(%q) = %v, want nil", addr, err)
		}
	}
}

func startAdmin(t *testing.T, cfg AdminConfig) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	view := &atomic.Pointer[laneState]{}
	view.Store(&laneState{})
	// The license handler answers 418 so a test can tell it was reached.
	teapot := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
	go serveAdmin(ctx, cfg, nil, nil, nil, nil, view, auditChain{}, nil, nil, nil, nil,
		teapot, slog.New(slog.DiscardHandler))
	base := "http://" + cfg.Listen
	waitFor(t, func() bool { code, _ := httpCall(t, http.MethodGet, base+"/healthz"); return code == http.StatusOK })
}

// Operators scrape /stats and /metrics on admin.listen and probe /healthz
// there. With no new key set, all of it must answer as before.
func TestAdminListenerServesEveryRouteByDefault(t *testing.T) {
	cfg := AdminConfig{Listen: freeTCPAddr(t)}
	startAdmin(t, cfg)
	base := "http://" + cfg.Listen
	for _, path := range []string{"/healthz", "/readyz", "/stats", "/metrics"} {
		if code, _ := httpCall(t, http.MethodGet, base+path); code != http.StatusOK {
			t.Errorf("GET %s on admin.listen = %d, want 200", path, code)
		}
	}
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{http.MethodPut, "/license", http.StatusTeapot},      // reaches the license handler
		{http.MethodPost, "/v1/drain", http.StatusForbidden}, // drain off without its token
		{http.MethodPost, "/healthz", http.StatusMethodNotAllowed},
		{http.MethodGet, "/license", http.StatusMethodNotAllowed},
	} {
		if code, _ := httpCall(t, tc.method, base+tc.path); code != tc.want {
			t.Errorf("%s %s on admin.listen = %d, want %d", tc.method, tc.path, code, tc.want)
		}
	}
}

// api_listen moves the read routes and leaves the probes where the platform
// polls them; the old port says where the routes went.
func TestAdminAPIListenMovesTheReadRoutes(t *testing.T) {
	cfg := AdminConfig{Listen: freeTCPAddr(t), APIListen: freeTCPAddr(t)}
	startAdmin(t, cfg)
	admin, api := "http://"+cfg.Listen, "http://"+cfg.APIListen
	waitFor(t, func() bool { code, _ := httpCall(t, http.MethodGet, api+"/stats"); return code == http.StatusOK })

	if code, _ := httpCall(t, http.MethodGet, admin+"/readyz"); code != http.StatusOK {
		t.Errorf("/readyz on admin.listen = %d, want 200", code)
	}
	for _, path := range []string{"/stats", "/metrics", "/config", "/events", "/api/sessions"} {
		if code, body := httpCall(t, http.MethodGet, admin+path); code != http.StatusNotFound || !strings.Contains(body, "admin.api_listen") {
			t.Errorf("GET %s on admin.listen = %d %q, want 404 naming admin.api_listen", path, code, body)
		}
	}
	if code, _ := httpCall(t, http.MethodGet, api+"/metrics"); code != http.StatusOK {
		t.Errorf("GET /metrics on admin.api_listen = %d, want 200", code)
	}
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/healthz"}, {http.MethodPut, "/license"}, {http.MethodPost, "/v1/drain"},
	} {
		if code, _ := httpCall(t, route.method, api+route.path); code != http.StatusNotFound {
			t.Errorf("%s %s on admin.api_listen = %d, want 404", route.method, route.path, code)
		}
	}
}

// disable_api turns the read routes off and keeps the probes.
func TestAdminDisableAPITurnsTheReadRoutesOff(t *testing.T) {
	cfg := AdminConfig{Listen: freeTCPAddr(t), DisableAPI: true}
	startAdmin(t, cfg)
	base := "http://" + cfg.Listen
	if code, _ := httpCall(t, http.MethodGet, base+"/readyz"); code != http.StatusOK {
		t.Errorf("/readyz with disable_api = %d, want 200", code)
	}
	for _, path := range []string{"/stats", "/metrics", "/config", "/events", "/api/sessions"} {
		if code, body := httpCall(t, http.MethodGet, base+path); code != http.StatusNotFound || !strings.Contains(body, "disable_api") {
			t.Errorf("GET %s with disable_api = %d %q, want 404 naming disable_api", path, code, body)
		}
	}
}

// api_listen on admin.listen's port is two servers on one socket, and
// beside disable_api it says the opposite: both are refused.
func TestAdminConfigValidate(t *testing.T) {
	for _, tc := range []struct {
		a    AdminConfig
		want string
	}{
		{AdminConfig{Listen: "0.0.0.0:19000", APIListen: "127.0.0.1:19000"}, "same port"},
		{AdminConfig{Listen: "127.0.0.1:19000", APIListen: "127.0.0.1:019000"}, "same port"},
		{AdminConfig{APIListen: "19002"}, "host:port"},
		{AdminConfig{Listen: "0.0.0.0:19000", APIListen: "127.0.0.1:19002", DisableAPI: true}, "one or the other"},
		{AdminConfig{Listen: "0.0.0.0:19000", APIListen: "127.0.0.1:19002"}, ""},
		{AdminConfig{Listen: "127.0.0.1:19000", APIListen: "10.0.0.5:19000"}, ""},
		{AdminConfig{Listen: "0.0.0.0:19000", DisableAPI: true}, ""},
		{AdminConfig{Listen: "0.0.0.0:19000"}, ""},
	} {
		got := strings.Join(tc.a.validate(), "; ")
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("validate(%+v) = %q, want %q", tc.a, got, tc.want)
		}
	}
}

// A health port that admin.api_listen also binds is refused like any other
// listener of the config.
func TestHealthListenRefusesTheAdminAPIPort(t *testing.T) {
	cfg := &Config{Admin: AdminConfig{Listen: "0.0.0.0:19000", APIListen: "127.0.0.1:19002"}}
	if err := checkHealthListen(cfg, "0.0.0.0:19002"); err == nil || !strings.Contains(err.Error(), "admin.api_listen") {
		t.Errorf("checkHealthListen on the API port = %v, want an error naming admin.api_listen", err)
	}
}

// A sidecar older than api_listen refuses a whole document that holds a key
// it cannot decode, so the plane must not serve it one.
func TestAdminAPIKeysAreNotServedToAnOlderSidecar(t *testing.T) {
	for name, cfg := range map[string]Config{
		"admin_api_listen":  {Admin: AdminConfig{Listen: "0.0.0.0:19000", APIListen: "127.0.0.1:19002"}},
		"admin_disable_api": {Admin: AdminConfig{Listen: "0.0.0.0:19000", DisableAPI: true}},
	} {
		if err := CheckServable(cfg, Handshake{Version: "1.306.0"}); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("CheckServable(%s) to 1.306.0 = %v, want a refusal naming it", name, err)
		}
		if err := CheckServable(cfg, Handshake{Capabilities: SidecarCapabilities()}); err != nil {
			t.Errorf("CheckServable(%s) to a current build = %v, want nil", name, err)
		}
	}
}

func freeTCPAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 5s")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func httpCall(t *testing.T, method, url string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}
