// Package config loads the control plane's configuration from the
// environment, once, at startup.
//
// Load returns a value. There is no package-level singleton and no Get(), so a
// test constructs a Config directly instead of resetting a global.
//
// A malformed value is a hard failure at startup. A missing optional value
// gets a documented default.
package config

import (
	"fmt"
	"os"
	"time"
)

// Config is the loaded configuration.
type Config struct {
	// ListenAddr is the HTTP bind address.
	ListenAddr string
	// ShutdownGrace bounds how long in-flight requests get on SIGTERM.
	ShutdownGrace time.Duration
}

const (
	// 8020 rather than 8009. The gateway owns 8009 and the two run side by
	// side on a development machine, so a colliding default would break every
	// existing setup on the first run.
	defaultListenAddr    = "0.0.0.0:8020"
	defaultShutdownGrace = 15 * time.Second
)

// Load reads the environment into a Config.
func Load() (Config, error) {
	var cfg Config
	var err error

	cfg.ListenAddr = envOr("CONTROLPLANE_LISTEN_ADDR", defaultListenAddr)

	if cfg.ShutdownGrace, err = durationFromEnv("CONTROLPLANE_SHUTDOWN_GRACE", defaultShutdownGrace); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func durationFromEnv(key string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("failed parsing %s, reason=%v", key, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s must be positive, got %q", key, v)
	}
	return d, nil
}
