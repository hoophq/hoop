// Package service installs, uninstalls, and inspects the hsh-tunneld
// system service across platforms.
//
// The package owns the *system-integration* surface of the daemon
// (RD-217). It is intentionally agnostic of how the daemon itself is
// implemented — its only inputs are paths (where the binary lives,
// where the config goes) and its only outputs are filesystem + service
// manager side effects (writing a unit, enabling it, starting it).
//
// Why a separate package
//
// We deliberately keep this package free of any tunnel-runtime imports
// (no netstack, no gRPC, no IPC) so that:
//
//   1. The `hsh-tunneld install` / `uninstall` / `validate-config` verbs
//      can run on a freshly-extracted tarball before any of the heavier
//      subsystems have ever been initialised — they are pure host-side
//      operations and should fail cleanly if e.g. the user is not root.
//
//   2. The Bun-side `hsh install` flow (in hoophq/hsh) can shell out to
//      `hsh-tunneld install` knowing that the work performed is exactly
//      the same as if a packager (brew, deb, rpm) had run the same
//      command from its post-install hook.
//
// Public surface
//
// The Manager interface is the contract for a single platform. Each
// platform has its own implementation:
//
//   - Linux (systemd) — file `service_linux.go`. Writes a unit to
//     /etc/systemd/system/, calls `systemctl daemon-reload`, `enable`,
//     and `start`. Implemented.
//
//   - macOS (LaunchDaemon) — file `service_darwin.go`. Stub for now;
//     returns ErrUnsupportedPlatform. Tracked as a follow-up to RD-217.
//
//   - Windows (svc/mgr) — file `service_windows.go`. Stub.
//
//   - Other (freebsd/openbsd/…) — file `service_other.go`. Stub.
//
// New returns the Manager appropriate for the current GOOS.
//
// Idempotency contract
//
// Every operation must be safe to re-run:
//
//   - Install on an already-installed system: refreshes the unit file
//     and ensures the service is enabled+running. Never fails because
//     it found existing artifacts.
//   - Uninstall on a not-installed system: returns nil. Never fails
//     because something was missing.
//   - Status: returns ErrNotInstalled when the service has never been
//     registered; never returns an error for an inactive-but-installed
//     service (that's StatusStopped).
//
// This matches what packagers, configuration-management tools (Ansible,
// Chef, Puppet), and end-users running `sudo hsh-tunneld install` twice
// all expect.
package service

import (
	"errors"
	"runtime"
)

// Status describes the current operational state of the system service.
//
// Three states is the minimum useful set: distinguishing "not installed"
// from "installed but stopped" is important for the UX (telling a user
// to run `sudo hsh-tunneld install` vs `sudo systemctl start
// hsh-tunneld` are different remediation paths). Distinguishing
// running-but-failing-checks (e.g. /api/serverinfo down) belongs to the
// daemon's own /v1/status endpoint, not here — we only report what the
// service manager itself knows.
type Status int

const (
	// StatusNotInstalled means there is no unit / plist / registry
	// entry for hsh-tunneld on this host. Recovery: run `install`.
	StatusNotInstalled Status = iota

	// StatusStopped means the service is registered but not currently
	// running. Recovery: start it (via systemctl/launchctl/sc) or via
	// Manager.Start.
	StatusStopped

	// StatusRunning means the service manager reports the daemon as
	// active. The daemon may still be unhealthy (no token, bad config)
	// — query /v1/status over IPC for that level of detail.
	StatusRunning
)

func (s Status) String() string {
	switch s {
	case StatusNotInstalled:
		return "not_installed"
	case StatusStopped:
		return "stopped"
	case StatusRunning:
		return "running"
	default:
		return "unknown"
	}
}

// Options configures an Install operation. The zero value is the
// production default for the current platform.
type Options struct {
	// BinaryPath is the absolute path of the hsh-tunneld binary the
	// service should execute. Empty means "use the platform default"
	// (/usr/local/bin/hsh-tunneld on POSIX).
	//
	// Install resolves a non-default value relative to the running
	// process's cwd before writing the unit file, so the resulting
	// unit always contains an absolute path. We refuse a non-absolute
	// BinaryPath rather than guessing.
	BinaryPath string

	// ConfigPath is the absolute path of the daemon's TOML config
	// file. Empty means "use the platform default"
	// (/etc/hsh/config.toml on POSIX).
	ConfigPath string

	// SocketPath is the absolute path of the IPC unix-socket the
	// daemon should bind. Empty means
	// /var/run/hsh/hsh.sock on POSIX. Embedded into the unit so the
	// daemon is launched with the same value the unprivileged hsh CLI
	// will probe.
	SocketPath string

	// GroupName is the OS group the runtime directory and socket are
	// chowned to. Members of this group can connect to the daemon
	// without sudo. Empty defaults to "hsh".
	//
	// The Install operation creates the group if it does not exist
	// (groupadd on Linux, dscl on macOS) and — when AddInvokingUser is
	// true — adds the human who ran `sudo hsh-tunneld install` to it so
	// the unprivileged `hsh` CLI / tray can talk to the daemon without
	// sudo afterward.
	GroupName string

	// CopyBinary controls whether Install copies the running
	// executable into BinaryPath before writing the unit. True (the
	// default) supports the "downloaded a tarball, ran sudo
	// ./hsh-tunneld install" workflow. False is for packagers (brew,
	// deb, rpm) whose post-install scripts have already laid the
	// binary down at BinaryPath; copying would race with their
	// integrity verification.
	CopyBinary bool

	// CreateGroup controls whether Install runs the platform-specific
	// "create group" command. Same rationale as CopyBinary — packagers
	// usually want to manage groups themselves through scriptlet hooks.
	// Defaults to true.
	CreateGroup bool

	// AddInvokingUser controls whether Install adds the human who ran
	// `sudo hsh-tunneld install` (resolved from $SUDO_USER) to GroupName.
	// This is what makes the post-install UX seamless: the unprivileged
	// `hsh` CLI and tray can read the control token + connect to the IPC
	// socket without sudo. Defaults to true.
	//
	// Caveat the installer must surface to the user: OS group membership
	// only takes effect for *new* login sessions, so a shell or tray that
	// was already running before install will not see the new group until
	// it is relaunched (or the user logs out / back in). Install never
	// errors when this step fails (e.g. $SUDO_USER unset because the
	// operator is a real root login, or a packager ran install with no
	// invoking user) — it logs the skip and continues, because a missing
	// group membership is recoverable after the fact and must not abort an
	// otherwise-successful service registration.
	//
	// Packagers (brew/deb/rpm) typically set this false and manage
	// membership through their own hooks, the same way they manage
	// CreateGroup.
	AddInvokingUser bool

	// EnableOnBoot controls whether the unit is enabled (so the
	// service starts at next boot) in addition to being started right
	// now. Defaults to true. Useful to set false in CI / one-shot
	// runs where the service should die with the test harness.
	EnableOnBoot bool

	// StartAfterInstall controls whether Install transitions the
	// service to Running once the unit is in place. Defaults to true.
	// CI / unattended installs that want to lay down the unit without
	// taking the host into a different runtime state can set false.
	StartAfterInstall bool
}

// PurgeOptions configures an Uninstall operation.
type PurgeOptions struct {
	// RemoveConfig deletes /etc/hsh/config.toml and the directory
	// itself if empty. False (default) preserves user state so a
	// subsequent reinstall keeps the operator logged in.
	RemoveConfig bool

	// RemoveBinary deletes the file at the resolved BinaryPath. False
	// (default) leaves the binary in place — uninstalling the service
	// should not also remove the user-facing `hsh-tunneld` command if
	// they want to keep it for `hsh-tunneld --version` style use.
	RemoveBinary bool

	// RemoveGroup runs `groupdel hsh` (or platform equivalent). False
	// (default) preserves the group so a reinstall does not have to
	// recreate it (and so any users we added to it stay in it).
	RemoveGroup bool

	// BinaryPath / ConfigPath / GroupName mirror their Options
	// counterparts. Empty resolves to the platform default.
	BinaryPath string
	ConfigPath string
	GroupName  string
}

// Manager is the abstraction over a single platform's service manager.
// Implementations live in service_<goos>.go; New picks the right one
// at runtime.
type Manager interface {
	// PlatformName returns a short human-readable identifier for the
	// underlying service manager ("systemd", "launchd", "windows", …).
	// Used in install banners and error messages.
	PlatformName() string

	// IsElevated reports whether the current process has sufficient
	// privileges to mutate the service manager. On POSIX this is
	// effectively `os.Geteuid() == 0`; on Windows it is membership of
	// the Administrators group / an elevated token.
	//
	// Callers should check this before any mutating operation and
	// short-circuit with a user-readable "please re-run as root" rather
	// than letting the underlying syscall fail mid-way through a
	// partial install.
	IsElevated() bool

	// Install registers the service with the platform manager and
	// (when StartAfterInstall is true) brings it to Running. It is
	// idempotent. See Options for the configurable knobs.
	Install(opts Options) error

	// Uninstall reverses Install. It is idempotent — calling on a
	// host that has never had hsh-tunneld installed returns nil.
	// PurgeOptions controls how aggressively user state is removed.
	Uninstall(opts PurgeOptions) error

	// Status reports the current service-manager-visible state of the
	// service. It never errors for "not installed" — that's a normal
	// state expressed via StatusNotInstalled.
	Status() (Status, error)

	// Start transitions the service from StatusStopped to
	// StatusRunning. Returns ErrNotInstalled if no unit exists. No-op
	// if already running.
	Start() error

	// Stop transitions the service from StatusRunning to
	// StatusStopped. Returns ErrNotInstalled if no unit exists. No-op
	// if already stopped.
	Stop() error
}

// Errors returned by Manager implementations. Callers should compare
// with errors.Is rather than ==.

var (
	// ErrUnsupportedPlatform is returned from New when the current
	// GOOS does not have a real implementation yet, and from every
	// stub method on the returned Manager.
	ErrUnsupportedPlatform = errors.New("service: not yet supported on this platform")

	// ErrNotElevated indicates the caller invoked a mutating method
	// (Install / Uninstall / Start / Stop) without sufficient
	// privilege. Wraps the platform-specific error from the service
	// manager call.
	ErrNotElevated = errors.New("service: insufficient privileges (re-run with sudo / from an elevated shell)")

	// ErrNotInstalled is returned from Start / Stop when no unit /
	// plist / registry entry exists. Distinct from a wrapped systemctl
	// "Failed to start" so callers can prompt for `install` instead of
	// re-running `start` with no chance of succeeding.
	ErrNotInstalled = errors.New("service: not installed")

	// ErrBinaryPathNotAbsolute indicates the caller passed a relative
	// BinaryPath. We refuse to guess what cwd they meant because the
	// unit file lives forever (until uninstall) and a relative path
	// inside a unit silently breaks the next time it's started from a
	// different cwd.
	ErrBinaryPathNotAbsolute = errors.New("service: binary path must be absolute")
)

// PlatformDefaults returns the recommended default Options values for
// the current GOOS. The Manager-specific implementations call this and
// overlay any non-zero fields from the caller. It is exposed so
// callers building their own Options can see what defaults they'd get
// and choose to override individual fields.
//
// On platforms where the daemon is not yet supported the returned
// values are still meaningful (we return what the install would use
// *if* it were supported) so error messages can reference them.
func PlatformDefaults() Options {
	switch runtime.GOOS {
	case "linux", "darwin":
		return Options{
			BinaryPath:        "/usr/local/bin/hsh-tunneld",
			ConfigPath:        "/etc/hsh/config.toml",
			SocketPath:        "/var/run/hsh/hsh.sock",
			GroupName:         "hsh",
			CopyBinary:        true,
			CreateGroup:       true,
			AddInvokingUser:   true,
			EnableOnBoot:      true,
			StartAfterInstall: true,
		}
	case "windows":
		return Options{
			BinaryPath:        `C:\Program Files\hsh\hsh-tunneld.exe`,
			ConfigPath:        `C:\ProgramData\hsh\config.toml`,
			SocketPath:        `\\.\pipe\hsh`,
			GroupName:         "", // not used on Windows
			CopyBinary:        true,
			CreateGroup:       false,
			AddInvokingUser:   false, // Windows uses a DACL granting local Users
			EnableOnBoot:      true,
			StartAfterInstall: true,
		}
	default:
		return Options{}
	}
}

// applyDefaults overlays PlatformDefaults onto opts. It fills only the
// fields the caller left at the zero value; explicit values win.
// Exported for tests in the same package — not part of the public API.
func applyDefaults(opts Options) Options {
	def := PlatformDefaults()
	if opts.BinaryPath == "" {
		opts.BinaryPath = def.BinaryPath
	}
	if opts.ConfigPath == "" {
		opts.ConfigPath = def.ConfigPath
	}
	if opts.SocketPath == "" {
		opts.SocketPath = def.SocketPath
	}
	if opts.GroupName == "" {
		opts.GroupName = def.GroupName
	}
	// Bool defaults need to be set explicitly by the caller via the
	// constructor below. Because Go has no nullable bool we can't
	// distinguish "false on purpose" from "left at the zero value"
	// here. New() solves this by always returning the platform-default
	// Options object and letting callers mutate individual booleans.
	return opts
}

// DefaultOptions returns the production install options for the current
// platform with sensible boolean defaults. Use this as the starting
// point for any programmatic install instead of constructing Options{}
// from scratch.
//
//	opts := service.DefaultOptions()
//	opts.CopyBinary = false   // packager already placed the binary
//	mgr := service.New()
//	if err := mgr.Install(opts); err != nil { … }
func DefaultOptions() Options {
	return PlatformDefaults()
}

// DefaultPurgeOptions returns the production uninstall options for the
// current platform. All "remove user state" booleans default to false.
func DefaultPurgeOptions() PurgeOptions {
	def := PlatformDefaults()
	return PurgeOptions{
		BinaryPath: def.BinaryPath,
		ConfigPath: def.ConfigPath,
		GroupName:  def.GroupName,
	}
}

// New returns the Manager for the current GOOS. The selection is
// compile-time on platforms with a real implementation (Linux) and a
// stub on the rest. We never return nil — a stub manager that returns
// ErrUnsupportedPlatform on every mutating method is more useful than
// a nil check at every call site.
func New() Manager {
	return newPlatformManager()
}
