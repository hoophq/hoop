package config

import (
	"testing"
	"time"
)

// clearEnv pins every variable Load reads, so the result does not depend on
// the developer's shell.
func clearEnv(t *testing.T) {
	t.Helper()
	t.Setenv("CONTROLPLANE_LISTEN_ADDR", "")
	t.Setenv("CONTROLPLANE_SHUTDOWN_GRACE", "")
}

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ListenAddr != defaultListenAddr {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, defaultListenAddr)
	}
	if cfg.ShutdownGrace != defaultShutdownGrace {
		t.Errorf("ShutdownGrace = %v, want %v", cfg.ShutdownGrace, defaultShutdownGrace)
	}
}

func TestLoadReadsOverrides(t *testing.T) {
	clearEnv(t)
	t.Setenv("CONTROLPLANE_LISTEN_ADDR", "127.0.0.1:9999")
	t.Setenv("CONTROLPLANE_SHUTDOWN_GRACE", "5s")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ListenAddr != "127.0.0.1:9999" {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, "127.0.0.1:9999")
	}
	if cfg.ShutdownGrace != 5*time.Second {
		t.Errorf("ShutdownGrace = %v, want %v", cfg.ShutdownGrace, 5*time.Second)
	}
}

// A malformed value stops the process while an operator is still watching,
// rather than surfacing as a confusing failure an hour later.
func TestLoadRejectsBadShutdownGrace(t *testing.T) {
	for _, v := range []string{"soon", "0s", "-1s"} {
		t.Run(v, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("CONTROLPLANE_SHUTDOWN_GRACE", v)

			if _, err := Load(); err == nil {
				t.Fatalf("Load() with %q returned no error", v)
			}
		})
	}
}
