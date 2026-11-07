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
		done <- serveHealth(ctx, addr, []endpointServer{lane}, []string{"desktops"}, slog.New(slog.DiscardHandler))
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

	err = serveHealth(context.Background(), held.Addr().String(), nil, nil, slog.New(slog.DiscardHandler))
	if err == nil || !strings.Contains(err.Error(), HealthListenEnv) {
		t.Fatalf("serveHealth on a held port = %v, want an error naming %s", err, HealthListenEnv)
	}
}

// A health port that another listener of the config also binds is refused
// before anything binds, naming the other listener. An ICA slot range counts
// as bound on every port it spans, and ports compare the way net.Listen
// resolves them.
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
		{"0.0.0.0:15432", "appdb"},
		{"0.0.0.0:19001", "desktops"},
		{"0.0.0.0:19003", "desktops"},
		{"0.0.0.0:19004", "desktops"},
		{"0.0.0.0:80", "web"},
		{"19001", "host:port"},
		{"0.0.0.0:no-such-service", "not a port number"},
	} {
		err := checkHealthListen(cfg, tc.addr)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("checkHealthListen(%q) = %v, want an error saying %q", tc.addr, err, tc.want)
		}
	}
	for _, addr := range []string{"", "0.0.0.0:19005", "0.0.0.0:19006", "127.0.0.1:0"} {
		if err := checkHealthListen(cfg, addr); err != nil {
			t.Errorf("checkHealthListen(%q) = %v, want nil", addr, err)
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
