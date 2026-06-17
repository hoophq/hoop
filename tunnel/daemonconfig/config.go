// Package daemonconfig persists the hsh-tunneld daemon's
// long-lived configuration to a TOML file on disk.
//
// What goes in here:
//
//   - The hoop API URL (so the daemon knows which gateway to talk to
//     across restarts).
//   - An optional explicit gRPC URL (otherwise auto-discovered from
//     /api/serverinfo at runtime).
//   - The OAuth access token persisted at the end of /v1/login/start
//     (RD-216).
//
// What does NOT go in here:
//
//   - The IPC control token (lives in /var/run/hsh/control-token and
//     rotates on every restart; see tunnel/ipc/auth.go).
//   - Any per-connection state.
//   - Any UI preferences (lives in `hsh` user-space config).
//
// On Linux/macOS the canonical path is /etc/hsh/config.toml owned by
// root and mode 0600. The daemon (running as root via the system
// service install in RD-217) is the only reader and writer. Dev runs
// override via the --config-file flag or the HSH_TUNNELD_CONFIG env
// var.
package daemonconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/BurntSushi/toml"
)

// DefaultConfigPath is the production location of the daemon config
// file on POSIX systems. The system-service installer (RD-217)
// creates /etc/hsh/ with root-owned 0750 permissions and writes an
// empty config.toml at install time.
//
// Windows uses %PROGRAMDATA%\hsh\config.toml; that is resolved at call
// time inside DefaultConfigPathPlatform to avoid pulling the
// environment lookup into a package-level const.
const DefaultConfigPath = "/etc/hsh/config.toml"

// DefaultConfigPathPlatform returns DefaultConfigPath on POSIX and the
// %PROGRAMDATA%-rooted equivalent on Windows. Kept platform-aware in
// one helper so the rest of the daemon does not branch on runtime.GOOS
// for path resolution.
//
// We deliberately do NOT honour HSH_TUNNELD_CONFIG here — that
// override is a CLI concern (the flag parser reads it) and bleeding
// env-var precedence into the path constants would make tests harder
// to reason about.
func DefaultConfigPathPlatform() string {
	if runtime.GOOS == "windows" {
		programData := os.Getenv("PROGRAMDATA")
		if programData == "" {
			programData = `C:\ProgramData`
		}
		return filepath.Join(programData, "hsh", "config.toml")
	}
	return DefaultConfigPath
}

// Config is the schema persisted to config.toml. Field names use snake
// case in TOML (matching the wire JSON shape exposed by /v1/config)
// so operators editing the file by hand do not have to mentally
// translate.
type Config struct {
	// APIURL is the hoop gateway's HTTPS API base, e.g.
	// "https://hoop.example.com". Required for the daemon to do
	// anything; without it `hsh-tunneld` fails fast at startup.
	APIURL string `toml:"api_url"`

	// GrpcURL, when non-empty, pins the gateway gRPC address. Empty
	// means "auto-discover via /api/serverinfo on every connect", which
	// is the recommended setting.
	GrpcURL string `toml:"grpc_url,omitempty"`

	// Token is the bearer token used to authenticate with the gateway.
	// Stored in plaintext: the file's 0600 permissions plus the
	// root-only path are the protection. Encrypting at rest gains us
	// nothing here because the daemon needs the cleartext at every
	// gateway dial.
	Token string `toml:"token,omitempty"`

	// LogLevel is one of "debug", "info", "warn", "error".
	// Empty means "info" — the default used by the daemon's
	// structured logger.
	LogLevel string `toml:"log_level,omitempty"`
}

// LoggedIn reports whether the config holds a non-empty access token.
// The exact validity of that token is decided by the gateway at dial
// time; this is the cheap "do we even have credentials" answer the
// /v1/status endpoint reports.
func (c Config) LoggedIn() bool {
	return c.Token != ""
}

// Load reads the config from disk. A missing file is treated as the
// empty Config (i.e. unconfigured), not an error — that lets the
// daemon boot the IPC layer for a fresh install where the user has
// not yet run `hsh login`.
//
// Any other error (permissions, corrupt TOML) IS returned so the
// operator sees the problem; the daemon should fail fast rather than
// silently treat a corrupt file as "logged out".
func Load(path string) (Config, error) {
	if path == "" {
		return Config{}, errors.New("daemonconfig: Load: path is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("daemonconfig: read %q: %w", path, err)
	}
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("daemonconfig: parse %q: %w", path, err)
	}
	return cfg, nil
}

// SaveOptions tunes the atomic-write behaviour. The defaults are
// correct for production; tests override Mode to avoid root-required
// permissions on shared CI runners.
type SaveOptions struct {
	// Mode is the file mode applied to the destination. Defaults to
	// 0600 — owner read/write only.
	Mode os.FileMode

	// DirMode is the mode applied to the containing directory if it
	// has to be created. Defaults to 0700.
	DirMode os.FileMode
}

// Save atomically persists the config to disk using the temp-file +
// rename pattern. Readers that open the file mid-write see either the
// previous content or the new content, never a torn payload.
//
// Failures here are fatal for the caller: if the daemon cannot
// persist the token after a login completes, returning success would
// silently lose the credentials at the next restart.
func Save(path string, cfg Config, opts SaveOptions) error {
	if path == "" {
		return errors.New("daemonconfig: Save: path is required")
	}
	if opts.Mode == 0 {
		opts.Mode = 0o600
	}
	if opts.DirMode == 0 {
		opts.DirMode = 0o700
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, opts.DirMode); err != nil {
		return fmt.Errorf("daemonconfig: ensure dir %q: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("daemonconfig: temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	enc := toml.NewEncoder(tmp)
	if err := enc.Encode(cfg); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("daemonconfig: encode TOML: %w", err)
	}
	if err := tmp.Chmod(opts.Mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("daemonconfig: chmod temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("daemonconfig: close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("daemonconfig: rename to %q: %w", path, err)
	}
	cleanup = false // rename succeeded; do not remove tmpPath
	return nil
}


