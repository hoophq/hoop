package daemon

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// The plane sets the interval on the answer. A served value replaces the
// wait already armed, so a millisecond cadence stops at once: one handshake
// in a window that would otherwise hold dozens, and the change is logged.
func TestTheHeartbeatHonorsTheServedInterval(t *testing.T) {
	rl, buf := testReloader(t, reloadBase)

	var mu sync.Mutex
	var seen int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen++
		mu.Unlock()
		w.Header().Set(HeartbeatSecondsHeader, "30")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(reloadBase))
	}))
	defer srv.Close()
	cp := &controlPlane{url: srv.URL, cred: tokenCredential("hsc_x"), every: time.Millisecond}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	cp.heartbeat(ctx, slog.New(slog.NewTextHandler(buf, nil)), rl)

	mu.Lock()
	defer mu.Unlock()
	if seen != 1 {
		t.Fatalf("want one handshake before the served 30s wait, got %d", seen)
	}
	if cp.every != 30*time.Second {
		t.Errorf("cp.every = %s, want 30s", cp.every)
	}
	if n := strings.Count(buf.String(), "control plane set the heartbeat interval"); n != 1 {
		t.Errorf("the change was logged %d times, want once:\n%s", n, buf)
	}
}

// A value outside the range, or not an integer, keeps the interval in
// force: the heartbeat keeps its millisecond cadence and says nothing.
func TestTheHeartbeatIgnoresABadServedInterval(t *testing.T) {
	for _, bad := range []string{"29", "3601", "abc", "60.5", "-60", ""} {
		t.Run(bad, func(t *testing.T) {
			rl, buf := testReloader(t, reloadBase)

			var mu sync.Mutex
			var seen int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				seen++
				mu.Unlock()
				w.Header().Set(HeartbeatSecondsHeader, bad)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(reloadBase))
			}))
			defer srv.Close()
			cp := &controlPlane{url: srv.URL, cred: tokenCredential("hsc_x"), every: time.Millisecond}

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
			defer cancel()
			cp.heartbeat(ctx, slog.New(slog.NewTextHandler(buf, nil)), rl)

			mu.Lock()
			defer mu.Unlock()
			if seen < 3 {
				t.Fatalf("want the cadence kept (at least three handshakes), got %d", seen)
			}
			if cp.every != time.Millisecond {
				t.Errorf("cp.every = %s, want the interval kept", cp.every)
			}
			if strings.Contains(buf.String(), "heartbeat interval") {
				t.Errorf("a refused value was logged as a change:\n%s", buf)
			}
		})
	}
}

// The boot handshake sets the interval the first wait uses.
func TestTheBootHandshakeSetsTheHeartbeatInterval(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(HeartbeatSecondsHeader, "3600")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(reloadBase))
	}))
	defer srv.Close()
	cfg, err := resolveConfigSource(nil, "hsc_x", srv.URL)
	if err != nil {
		t.Fatalf("resolveConfigSource: %v", err)
	}
	if cfg.cp.every != time.Hour || cfg.cp.interval() != time.Hour {
		t.Errorf("boot interval = %s, want 1h", cfg.cp.every)
	}
}
