// Package config loads the control plane's configuration from the
// environment, once, at startup.
//
// Load returns a value. There is no package-level singleton and no Get().
// The gateway has one, and the cost shows up first in tests: a global has to
// be reset between cases, and a struct with unexported fields cannot be
// constructed by the package under test at all. Everything here is exported
// and a caller threads the value where it is needed, so a test writes
// config.Config{ListenAddr: ..., CORSAllowedOrigins: ...} and is done.
//
// The rule Load enforces is the part worth keeping from the gateway: a
// malformed value is a hard failure at startup, a missing optional value gets
// a documented default. Config that is wrong should stop the process while an
// operator is still watching, not surface as a confusing 500 an hour later.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hoophq/hoop/controlplane/backend/internal/database"
)

// Config is the loaded configuration.
//
// Copy it freely, but note CORSAllowedOrigins is a slice: a copy shares the
// backing array. Nothing mutates it after Load, and Server holds it read-only.
type Config struct {
	// ListenAddr is the HTTP bind address.
	ListenAddr string
	// PostgresURI is the connection URI for the state store.
	PostgresURI string
	// MaxOpenConns caps the connection pool. Zero means unlimited.
	MaxOpenConns int
	// MigrationPathFiles reads migrations from disk instead of the embedded
	// copy. Empty means embedded.
	MigrationPathFiles string
	// Deployment marks how seriously the process should treat itself.
	Deployment DeploymentType
	// CORSAllowedOrigins is the exact-match allow list. Empty means no
	// cross-origin request is allowed. See httpapi.cors for why.
	CORSAllowedOrigins []string
	// ShutdownGrace bounds how long in-flight requests get on SIGTERM.
	ShutdownGrace time.Duration
	// AutoMigrate applies pending migrations during `serve`. True by default
	// so a first run works; set false where a deploy pipeline runs
	// `controlplane migrate up` as its own step.
	AutoMigrate bool
}

// DeploymentType marks how seriously the process should treat itself.
//
// It exists for one reason, named in EVL-234: the development trust anchor
// for sidecar auth is a static shared token, and a placeholder that quietly
// becomes the shipping default is how a placeholder turns into a CVE. Code
// holding a development credential asks IsProduction and refuses to start
// when it is true. That check is worthless unless the value is explicit, so
// there is no inference from other settings.
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

	// 8020 rather than 8009. The gateway owns 8009 and the two run side by
	// side for the whole of the 2.0 transition, so a colliding default would
	// break every existing development machine on the first run.
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

// postgresURIFromEnv reads and checks POSTGRES_DB_URI.
//
// Checked here, at load, so the failure names the variable. The driver's own
// message does not, which sends the operator looking in the wrong place.
// database.ParseURI is what redacts the credential out of the parse error.
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
		// Defaulting to development is the safe direction: the value only ever
		// unlocks refusals, so an unset variable must not be the one that
		// disables them. A production deployment says so.
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
