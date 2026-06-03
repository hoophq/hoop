package main

import (
	"context"
	"errors"
	"io"
	"log"
	"testing"

	"github.com/hoophq/hoop/tunnel/daemonconfig"
	"github.com/hoophq/hoop/tunnel/ipc"
	"github.com/hoophq/hoop/tunnel/tunnelmgr"
)

// newTestService builds a daemonService backed by a freshly-constructed
// Manager (which starts Idle and performs no I/O until BringUp). The
// config path is empty so nothing touches disk. initialCfg controls the
// logged-in state via its Token field.
func newTestService(t *testing.T, initialCfg daemonconfig.Config) *daemonService {
	t.Helper()
	mgr, err := tunnelmgr.New(tunnelmgr.Options{
		Logger:      log.New(io.Discard, "", 0),
		SessionSeed: "test-seed",
		TLD:         "hoop",
		UserAgent:   "hsh-tunneld-test",
	})
	if err != nil {
		t.Fatalf("tunnelmgr.New: %v", err)
	}
	svc, err := newDaemonService(daemonServiceOptions{
		Manager:       mgr,
		ParentContext: context.Background(),
		ConfigPath:    "", // in-memory: do not persist
		InitialConfig: initialCfg,
	})
	if err != nil {
		t.Fatalf("newDaemonService: %v", err)
	}
	return svc
}

// TestService_UpWhileLoggedOut verifies that Up refuses to bring the
// tunnel online when there is no token, returning the ErrNotLoggedIn
// sentinel so the HTTP layer can map it to 409.
func TestService_UpWhileLoggedOut(t *testing.T) {
	svc := newTestService(t, daemonconfig.Config{APIURL: "https://gw.example.com"})

	_, err := svc.Up(context.Background())
	if !errors.Is(err, ipc.ErrNotLoggedIn) {
		t.Fatalf("Up() err = %v, want ErrNotLoggedIn", err)
	}
}

// TestService_DownWhenIdleIsNoOp verifies Down is idempotent: tearing
// down an already-idle daemon succeeds and reports AlreadyDown=true
// without error.
func TestService_DownWhenIdleIsNoOp(t *testing.T) {
	svc := newTestService(t, daemonconfig.Config{})

	resp, err := svc.Down(context.Background())
	if err != nil {
		t.Fatalf("Down() err = %v, want nil", err)
	}
	if !resp.AlreadyDown {
		t.Errorf("AlreadyDown = false, want true (manager was idle)")
	}
}

// TestService_RefreshWhenDownIsNoOp verifies that a refresh against a
// down tunnel reports Running=false and does not error — the periodic
// loop relies on this to tick harmlessly while logged out.
func TestService_RefreshWhenDownIsNoOp(t *testing.T) {
	svc := newTestService(t, daemonconfig.Config{
		APIURL: "https://gw.example.com",
		Token:  "tok-123",
	})

	resp, err := svc.RefreshConnections(context.Background())
	if err != nil {
		t.Fatalf("RefreshConnections() err = %v, want nil", err)
	}
	if resp.Running {
		t.Errorf("Running = true, want false (tunnel is down)")
	}
	if resp.Count != 0 {
		t.Errorf("Count = %d, want 0", resp.Count)
	}
}

// TestManager_RefreshWhenIdleIsNoOp verifies the Manager-level guard:
// Refresh against an idle manager returns nil without touching the
// gateway (no panic, no fetch).
func TestManager_RefreshWhenIdleIsNoOp(t *testing.T) {
	svc := newTestService(t, daemonconfig.Config{})
	if err := svc.mgr.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh() on idle manager err = %v, want nil", err)
	}
}

// TestService_DownStaysLoggedIn verifies the lifecycle/auth separation:
// taking the tunnel down must NOT clear the token. The user remains
// logged in and can bring the tunnel back up without re-authenticating.
func TestService_DownStaysLoggedIn(t *testing.T) {
	svc := newTestService(t, daemonconfig.Config{
		APIURL: "https://gw.example.com",
		Token:  "tok-123",
	})

	if _, err := svc.Down(context.Background()); err != nil {
		t.Fatalf("Down() err = %v", err)
	}

	st, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() err = %v", err)
	}
	if !st.LoggedIn {
		t.Errorf("LoggedIn = false after Down, want true (Down must not clear the token)")
	}
}
