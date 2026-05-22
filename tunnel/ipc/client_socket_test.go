//go:build !windows

package ipc

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestClient_EndToEndOverUnixSocket exercises the full stack:
// Listen → Server.Serve → real net.Dialer in the Client → response
// decoded back into typed Go values. It catches any wiring mistake in
// either side (wrong content-type, missing auth header, JSON shape
// drift) that would slip past the in-process Handler() tests.
func TestClient_EndToEndOverUnixSocket(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "hsh-test.sock")

	svc := &fakeService{
		statusResp: StatusResponse{
			Running:       true,
			LoggedIn:      true,
			DaemonVersion: "test-version",
		},
		connsResp: []Connection{
			{Name: "pg-prod", SubType: "postgres", VirtualIP: "fd00::1", ExpectedPort: 5432},
		},
	}

	var store MemoryTokenStore
	tok, err := store.Rotate()
	if err != nil {
		t.Fatal(err)
	}

	srv, err := NewServer(ServerOptions{
		Service:    svc,
		TokenStore: &store,
		Logger:     log.New(io.Discard, "", 0),
	})
	if err != nil {
		t.Fatal(err)
	}

	ln, err := Listen(ListenerOptions{Path: sockPath, Mode: 0o600})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer func() {
		// Best-effort cleanup; if Shutdown ran first the listener is
		// already closed.
		_ = ln.Close()
	}()

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	// Give Serve a moment to start accepting. The Listen call already
	// has the socket file in place so we don't need to poll for that.
	time.Sleep(20 * time.Millisecond)

	client, err := NewClient(ClientOptions{
		SocketPath: sockPath,
		Token:      tok.Raw(),
		Timeout:    2 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	status, err := client.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Running || !status.LoggedIn || status.DaemonVersion != "test-version" {
		t.Errorf("Status response = %+v", status)
	}

	conns, err := client.Connections(ctx)
	if err != nil {
		t.Fatalf("Connections: %v", err)
	}
	if len(conns) != 1 || conns[0].Name != "pg-prod" {
		t.Errorf("Connections = %+v", conns)
	}

	// Shutdown is graceful: Serve must return nil (ErrServerClosed is
	// squashed). Give it a deadline so a stuck shutdown fails the test.
	shutdownCtx, sCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer sCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	select {
	case err := <-serveErr:
		if err != nil {
			t.Fatalf("Serve returned non-nil error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after Shutdown")
	}
}

func TestClient_UnauthorizedSurfacesAPIError(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "hsh-test.sock")

	var store MemoryTokenStore
	if _, err := store.Rotate(); err != nil {
		t.Fatal(err)
	}

	srv, _ := NewServer(ServerOptions{
		Service:    &fakeService{},
		TokenStore: &store,
		Logger:     log.New(io.Discard, "", 0),
	})
	ln, err := Listen(ListenerOptions{Path: sockPath, Mode: 0o600})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Shutdown(context.Background()) }()
	time.Sleep(20 * time.Millisecond)

	client, err := NewClient(ClientOptions{
		SocketPath: sockPath,
		Token:      "this-is-not-the-right-token",
		Timeout:    2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.Status(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !IsUnauthorized(err) {
		t.Errorf("IsUnauthorized = false, err = %v", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not APIError: %v", err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d, want 401", apiErr.StatusCode)
	}
}

func TestListen_RefusesNonSocketPath(t *testing.T) {
	// Path points at a regular file; Listen must refuse rather than
	// overwriting it.
	dir := t.TempDir()
	path := filepath.Join(dir, "regular-file")
	if err := os.WriteFile(path, []byte("important"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Listen(ListenerOptions{Path: path, Mode: 0o600})
	if err == nil {
		t.Fatal("Listen succeeded over a regular file")
	}
}

func TestListen_RemovesStaleSocket(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hsh-test.sock")

	// Bind once and close immediately, leaving the socket file behind.
	ln1, err := Listen(ListenerOptions{Path: path, Mode: 0o600})
	if err != nil {
		t.Fatal(err)
	}
	_ = ln1.Close()

	// The file still exists; second Listen should clean it up and bind.
	ln2, err := Listen(ListenerOptions{Path: path, Mode: 0o600})
	if err != nil {
		t.Fatalf("Listen over stale socket: %v", err)
	}
	defer ln2.Close()
}
