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

// admin.listen is what platforms probe, so it must answer the probes and
// serve none of the unauthenticated read routes; those answer on
// admin.api_listen only.
func TestAdminListenerServesProbesAndAPIListenerServesReads(t *testing.T) {
	addr, apiAddr := freeTCPAddr(t), freeTCPAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	view := &atomic.Pointer[laneState]{}
	view.Store(&laneState{})
	go serveAdmin(ctx, addr, apiAddr, nil, nil, nil, nil, view, auditChain{}, nil, nil, nil, nil,
		slog.New(slog.DiscardHandler), false)

	admin, api := "http://"+addr, "http://"+apiAddr
	waitFor(t, func() bool { code, _ := httpCall(t, http.MethodGet, admin+"/healthz"); return code == http.StatusOK })
	waitFor(t, func() bool { code, _ := httpCall(t, http.MethodGet, api+"/stats"); return code == http.StatusOK })

	if code, _ := httpCall(t, http.MethodGet, admin+"/readyz"); code != http.StatusOK {
		t.Errorf("/readyz on admin.listen = %d, want 200", code)
	}
	// PUT /license was removed: nothing on either listener installs one.
	if code, _ := httpCall(t, http.MethodPut, admin+"/license"); code != http.StatusNotFound && code != http.StatusMethodNotAllowed {
		t.Errorf("PUT /license on admin.listen = %d, want 404 or 405", code)
	}
	for _, path := range []string{"/stats", "/metrics", "/config", "/events", "/api/sessions"} {
		if code, _ := httpCall(t, http.MethodGet, admin+path); code != http.StatusNotFound {
			t.Errorf("GET %s on admin.listen = %d, want 404", path, code)
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

// An admin.api_listen that cannot bind stops the process: admin.listen must
// not answer probes while the read routes never started.
func TestAdminAPIListenBindFailureIsReturned(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	addr := freeTCPAddr(t)
	view := &atomic.Pointer[laneState]{}
	view.Store(&laneState{})
	done := make(chan error, 1)
	go func() {
		done <- serveAdmin(context.Background(), addr, held.Addr().String(), nil, nil, nil, nil, view, auditChain{},
			nil, nil, nil, nil, slog.New(slog.DiscardHandler), false)
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "admin.api_listen") {
			t.Fatalf("serveAdmin with a held api_listen = %v, want an error naming admin.api_listen", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serveAdmin kept running with a held api_listen")
	}
	// admin.listen was released, not left serving probes.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("admin.listen still held after the failure: %v", err)
	}
	_ = ln.Close()
}

// api_listen on admin.listen's port is two servers on one socket: one would
// fail to bind, so the config is refused.
func TestAdminAPIListenMustNotShareTheAdminPort(t *testing.T) {
	for _, tc := range []struct {
		a    AdminConfig
		want string
	}{
		{AdminConfig{Listen: "0.0.0.0:19000", APIListen: "127.0.0.1:19000"}, "same port"},
		{AdminConfig{Listen: "127.0.0.1:19000", APIListen: "127.0.0.1:019000"}, "same port"},
		{AdminConfig{APIListen: "19002"}, "host:port"},
		{AdminConfig{Listen: "0.0.0.0:19000", APIListen: "127.0.0.1:19002"}, ""},
		{AdminConfig{Listen: "127.0.0.1:19000", APIListen: "10.0.0.5:19000"}, ""},
		{AdminConfig{APIListen: "127.0.0.1:19002"}, ""},
	} {
		got := strings.Join(tc.a.validate(), "; ")
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("validate(%+v) = %q, want %q", tc.a, got, tc.want)
		}
	}
}

// With api_listen unset, the read routes are off, and a scraper still pointed
// at admin.listen reads why instead of a bare 404.
func TestAdminListenerAloneSaysWhereTheReadRoutesWent(t *testing.T) {
	addr := freeTCPAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	view := &atomic.Pointer[laneState]{}
	view.Store(&laneState{})
	go serveAdmin(ctx, addr, "", nil, nil, nil, nil, view, auditChain{}, nil, nil, nil, nil,
		slog.New(slog.DiscardHandler), false)

	base := "http://" + addr
	waitFor(t, func() bool { code, _ := httpCall(t, http.MethodGet, base+"/healthz"); return code == http.StatusOK })
	for _, path := range []string{"/stats", "/metrics", "/config", "/events", "/api/sessions"} {
		code, body := httpCall(t, http.MethodGet, base+path)
		if code != http.StatusNotFound || !strings.Contains(body, "admin.api_listen") {
			t.Errorf("GET %s on admin.listen = %d %q, want 404 naming admin.api_listen", path, code, body)
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
func TestAdminAPIListenIsNotServedToAnOlderSidecar(t *testing.T) {
	cfg := Config{Admin: AdminConfig{Listen: "0.0.0.0:19000", APIListen: "127.0.0.1:19002"}}
	if err := CheckServable(cfg, Handshake{Version: "1.306.0"}); err == nil || !strings.Contains(err.Error(), "admin_api_listen") {
		t.Errorf("CheckServable to 1.306.0 = %v, want a refusal naming admin_api_listen", err)
	}
	if err := CheckServable(cfg, Handshake{Capabilities: SidecarCapabilities()}); err != nil {
		t.Errorf("CheckServable to a current build = %v, want nil", err)
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
