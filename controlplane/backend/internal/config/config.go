// Package config loads the control plane's configuration from the
// environment, once, at startup.
//
// Load returns a value; there is no singleton and no Get(), so tests just
// construct config.Config{...}. Malformed values fail hard at startup;
// missing optional values get documented defaults.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hoophq/hoop/controlplane/backend/internal/database"
)

// Config is the loaded configuration. Copy freely; CORSAllowedOrigins shares
// its backing array, but nothing mutates it after Load.
type Config struct {
	// ListenAddr is the HTTP bind address.
	ListenAddr string
	// PostgresURI is the connection URI for the state store.
	PostgresURI string
	// MaxOpenConns caps the connection pool. Zero means unlimited.
	MaxOpenConns int
	// MigrationPathFiles reads migrations from disk; empty means embedded.
	MigrationPathFiles string
	// Deployment marks how seriously the process should treat itself.
	Deployment DeploymentType
	// CORSAllowedOrigins is the exact-match allow list. Empty means no
	// cross-origin request is allowed.
	CORSAllowedOrigins []string
	// ShutdownGrace bounds how long in-flight requests get on SIGTERM.
	ShutdownGrace time.Duration
	// AutoMigrate applies pending migrations during `serve`. Default true;
	// set false when a pipeline runs `controlplane migrate up` itself.
	AutoMigrate bool
}

// DeploymentType marks how seriously the process should treat itself.
//
// The development trust anchor for sidecar auth is a static shared token,
// and a placeholder that becomes the shipping default is a CVE. Code holding
// a development credential asks IsProduction and refuses to start when true;
// the value is explicit, never inferred from other settings.
type DeploymentType string

const (
	DeploymentDevelopment DeploymentType = "development"
	DeploymentProduction  DeploymentType = "production"
)

const (
	defaultListenAddr    = "0.0.0.0:8020"
	defaultShutdownGrace = 15 * time.Second
)

// Load reads the environment into a Config.
func Load() (Config, error) {
	var cfg Config

	// 8020, not 8009: the gateway owns 8009 and both run side by side on a
	// development machine.
	cfg.ListenAddr = envOr("CONTROLPLANE_LISTEN_ADDR", defaultListenAddr)

	postgresURI, err := postgresURIFromEnv()
	if err != nil {
		return Config{}, err
	}
	cfg.PostgresURI = postgresURI

	if cfg.MaxOpenConns, err = intFromEnv("CONTROLPLANE_MAX_OPEN_CONNS", 0); err != nil {
		return Config{}, err
	}
	cfg.MigrationPathFiles = os.Getenv("CONTROLPLANE_MIGRATION_PATH_FILES")

	if cfg.Deployment, err = deploymentFromEnv(); err != nil {
		return Config{}, err
	}

	cfg.CORSAllowedOrigins = splitAndTrim(os.Getenv("CONTROLPLANE_CORS_ALLOWED_ORIGINS"))

	if cfg.ShutdownGrace, err = durationFromEnv("CONTROLPLANE_SHUTDOWN_GRACE", defaultShutdownGrace); err != nil {
		return Config{}, err
	}

	if cfg.AutoMigrate, err = boolFromEnv("CONTROLPLANE_AUTO_MIGRATE", true); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// IsProduction reports whether the deployment is marked production.
func (c Config) IsProduction() bool { return c.Deployment == DeploymentProduction }

// postgresURIFromEnv reads and checks POSTGRES_DB_URI at load, so the
// failure names the variable; database.ParseURI redacts the credential.
func postgresURIFromEnv() (string, error) {
	const key = "POSTGRES_DB_URI"
	v := os.Getenv(key)
	if v == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	if _, err := database.ParseURI(v); err != nil {
		return "", fmt.Errorf("%s: %w", key, err)
	}
	return v, nil
}

func deploymentFromEnv() (DeploymentType, error) {
	const key = "CONTROLPLANE_DEPLOYMENT"
	switch d := DeploymentType(strings.ToLower(strings.TrimSpace(os.Getenv(key)))); d {
	case "":
		// Development is the safe default: the value only unlocks refusals,
		// so an unset variable must not disable them.
		return DeploymentDevelopment, nil
	case DeploymentDevelopment, DeploymentProduction:
		return d, nil
	default:
		return "", fmt.Errorf("invalid %s %q (want development or production)", key, d)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func intFromEnv(key string, def int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("failed parsing %s, reason=%v", key, err)
	}
	if n < 0 {
		return 0, fmt.Errorf("%s must not be negative, got %q", key, v)
	}
	return n, nil
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

func boolFromEnv(key string, def bool) (bool, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("failed parsing %s, reason=%v", key, err)
	}
	return b, nil
}

func splitAndTrim(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
