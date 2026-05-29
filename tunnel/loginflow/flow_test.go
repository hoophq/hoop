package loginflow

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newTestFlow assembles a Flow with stubbed gateway calls (so tests
// don't reach real hoop) and a freshly-allocated callback port (so
// parallel tests don't trip the singleton-port restriction).
func newTestFlow(t *testing.T, onSuccess PersistFn) (*Flow, *uint32) {
	t.Helper()
	port := allocPort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	var loginURLHits uint32
	f, err := New(Options{
		APIURL:       "http://fake-gateway",
		OnSuccess:    onSuccess,
		CallbackAddr: addr,
		Timeout:      2 * time.Second,
		AuthMethod: func(context.Context, string) (string, error) {
			return "oidc", nil
		},
		LoginURL: func(context.Context, string, string) (string, error) {
			atomic.AddUint32(&loginURLHits, 1)
			return "https://gateway.example/oauth?fake=1", nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return f, &loginURLHits
}

// allocPort grabs a free TCP port by binding+closing. There is a small
// race window where the port could be reclaimed before the test
// connects, but in practice it's reliable enough for unit tests.
func allocPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("alloc port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// fireCallback simulates the browser hitting the gateway-redirected
// /callback URL.
func fireCallback(t *testing.T, addr string, query string) *http.Response {
	t.Helper()
	url := "http://" + addr + "/callback"
	if query != "" {
		url += "?" + query
	}
	// We deliberately reach the callback server on the same port the
	// flow bound; allow a few retries because Serve runs on a goroutine
	// and may not be ready the instant Start returns.
	var lastErr error
	for i := 0; i < 20; i++ {
		resp, err := http.Get(url)
		if err == nil {
			return resp
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("could not reach callback server: %v", lastErr)
	return nil
}

func TestStart_HappyPath(t *testing.T) {
	var saved string
	var savedOnce sync.Once
	f, hits := newTestFlow(t, func(token string) error {
		savedOnce.Do(func() { saved = token })
		return nil
	})

	url, state, err := f.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if url == "" || state == "" {
		t.Fatalf("Start returned empty values: url=%q state=%q", url, state)
	}
	if *hits != 1 {
		t.Errorf("LoginURL fetched %d times, want 1", *hits)
	}

	// Pre-callback: status should be pending.
	if r, ok := f.Poll(state); !ok || r.Status != StatusPending {
		t.Errorf("pre-callback Poll: ok=%v result=%+v", ok, r)
	}

	// Simulate the browser callback.
	resp := fireCallback(t, f.CallbackAddr(), "token=secret-jwt-token")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("callback status=%d body=%s", resp.StatusCode, string(body))
	}

	// Post-callback: status should be done.
	if r, ok := f.Poll(state); !ok || r.Status != StatusDone {
		t.Errorf("post-callback Poll: ok=%v result=%+v", ok, r)
	}
	if saved != "secret-jwt-token" {
		t.Errorf("OnSuccess received token=%q, want secret-jwt-token", saved)
	}
}

func TestStart_RejectsConcurrentStart(t *testing.T) {
	f, _ := newTestFlow(t, func(string) error { return nil })

	_, state1, err := f.Start(context.Background())
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	_, _, err = f.Start(context.Background())
	if err == nil {
		t.Fatal("second Start should fail while first is pending")
	}
	if err != ErrFlowInProgress {
		t.Errorf("err = %v, want ErrFlowInProgress", err)
	}
	if r, _ := f.Poll(state1); r.Status != StatusPending {
		t.Errorf("first attempt should still be pending: %+v", r)
	}
	// Clean up so the test does not leak the listener.
	f.Cancel()
}

func TestCallback_ErrorParam(t *testing.T) {
	f, _ := newTestFlow(t, func(string) error {
		t.Fatal("OnSuccess must not run when callback delivers an error")
		return nil
	})

	_, state, err := f.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	resp := fireCallback(t, f.CallbackAddr(), "error=access_denied")
	defer resp.Body.Close()

	r, ok := f.Poll(state)
	if !ok {
		t.Fatal("Poll: state unknown")
	}
	if r.Status != StatusError {
		t.Errorf("status = %v, want error", r.Status)
	}
	if r.Error != "access_denied" {
		t.Errorf("error = %q, want access_denied", r.Error)
	}
}

func TestCallback_EmptyToken(t *testing.T) {
	f, _ := newTestFlow(t, func(string) error { return nil })
	_, state, err := f.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	resp := fireCallback(t, f.CallbackAddr(), "")
	defer resp.Body.Close()
	r, _ := f.Poll(state)
	if r.Status != StatusError {
		t.Errorf("status = %v, want error", r.Status)
	}
	if !strings.Contains(r.Error, "without a token") {
		t.Errorf("error = %q, want substring 'without a token'", r.Error)
	}
}

func TestCallback_PersistFailure(t *testing.T) {
	f, _ := newTestFlow(t, func(string) error {
		return fmt.Errorf("disk full")
	})
	_, state, err := f.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	resp := fireCallback(t, f.CallbackAddr(), "token=jwt")
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "disk full") {
		t.Errorf("browser response = %q, want substring 'disk full'", string(body))
	}
	r, _ := f.Poll(state)
	if r.Status != StatusError {
		t.Errorf("status = %v, want error", r.Status)
	}
	if !strings.Contains(r.Error, "disk full") {
		t.Errorf("error = %q, want substring 'disk full'", r.Error)
	}
}

func TestTimeout(t *testing.T) {
	f, _ := newTestFlow(t, func(string) error { return nil })
	// Override timeout to something very short for the test.
	f.opts.Timeout = 100 * time.Millisecond

	_, state, err := f.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Wait until the timeout watcher kicks in.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if r, _ := f.Poll(state); r.Status == StatusError {
			if !strings.Contains(r.Error, "timed out") {
				t.Errorf("error = %q, want substring 'timed out'", r.Error)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("flow did not transition to error after timeout")
}

func TestPoll_UnknownState(t *testing.T) {
	f, _ := newTestFlow(t, func(string) error { return nil })
	if _, ok := f.Poll("nope"); ok {
		t.Error("Poll on unknown state should return ok=false")
	}
}

func TestCancel(t *testing.T) {
	f, _ := newTestFlow(t, func(string) error { return nil })
	_, state, err := f.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.Cancel()
	r, _ := f.Poll(state)
	if r.Status != StatusError {
		t.Errorf("status = %v, want error", r.Status)
	}
	if !strings.Contains(r.Error, "cancelled") {
		t.Errorf("error = %q, want substring 'cancelled'", r.Error)
	}
	// A new Start should now succeed.
	if _, _, err := f.Start(context.Background()); err != nil {
		t.Errorf("Start after Cancel: %v", err)
	}
}

func TestNew_RejectsMissingFields(t *testing.T) {
	if _, err := New(Options{OnSuccess: func(string) error { return nil }}); err == nil {
		t.Error("New without APIURL should fail")
	}
	if _, err := New(Options{APIURL: "http://x"}); err == nil {
		t.Error("New without OnSuccess should fail")
	}
}

func TestDefaultGatewayHelpers(t *testing.T) {
	// Stand up a tiny fake gateway and verify the default
	// AuthMethod / LoginURL functions hit the right paths and pick
	// the right fields out of the response.
	mux := http.NewServeMux()
	mux.HandleFunc("/api/publicserverinfo", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"auth_method":"oidc","other":"stuff"}`)
	})
	mux.HandleFunc("/api/login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"login_url":"https://gateway.example/oauth"}`)
	})
	mux.HandleFunc("/api/saml/login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"login_url":"https://gateway.example/saml"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	got, err := defaultFetchAuthMethod(client)(context.Background(), srv.URL)
	if err != nil || got != "oidc" {
		t.Errorf("AuthMethod = %q, %v; want oidc, nil", got, err)
	}

	url, err := defaultFetchLoginURL(client)(context.Background(), srv.URL, "oidc")
	if err != nil || url != "https://gateway.example/oauth" {
		t.Errorf("LoginURL oidc = %q, %v", url, err)
	}
	url, err = defaultFetchLoginURL(client)(context.Background(), srv.URL, "saml")
	if err != nil || url != "https://gateway.example/saml" {
		t.Errorf("LoginURL saml = %q, %v", url, err)
	}
}
