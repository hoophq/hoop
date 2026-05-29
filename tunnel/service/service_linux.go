//go:build linux

package service

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
)

// newPlatformManager returns the Linux/systemd implementation.
// Always non-nil; failure to reach systemctl at runtime is reported by
// the individual operations rather than at construction time so unit
// tests can still introspect the manager when systemctl is unavailable.
func newPlatformManager() Manager {
	return &linuxManager{
		unitDir:  "/etc/systemd/system",
		unitName: "hsh-tunneld.service",
	}
}

// linuxManager is the systemd-backed Manager. All shelling out goes
// through this type so individual methods are small wrappers around
// runSystemctl, and the test suite can replace systemctlPath with a
// fake binary.
type linuxManager struct {
	unitDir  string // /etc/systemd/system
	unitName string // hsh-tunneld.service

	// systemctlPath overrides exec.LookPath("systemctl") in tests.
	systemctlPath string
	// groupaddPath / groupdelPath / usermodPath override the same way.
	groupaddPath string
	groupdelPath string
	usermodPath  string
}

func (l *linuxManager) PlatformName() string { return "systemd" }

func (l *linuxManager) IsElevated() bool { return os.Geteuid() == 0 }

// unitPath is the absolute path of the systemd unit file the manager
// owns. Centralised here so renames in the future only touch one line.
func (l *linuxManager) unitPath() string {
	return filepath.Join(l.unitDir, l.unitName)
}

// Install lays down the unit and (optionally) starts the service.
//
// The order of operations matters when something fails partway through:
//
//  1. Validate inputs (binary path absolute, executable exists if not
//     CopyBinary, …) — pure-Go, no side effects on the host.
//  2. Create the hsh group — idempotent, leaves a single new gid on
//     the host even if the rest fails. Tolerable.
//  3. Copy the binary — writes /usr/local/bin/hsh-tunneld. On failure
//     before this point: nothing happened on disk. On failure here:
//     we have the binary in place but no unit, which is harmless.
//  4. Touch the config — ensures validate-config has something to
//     read in ExecStartPre.
//  5. Write the unit + daemon-reload — at this point the service is
//     registered. A failure after this leaves the user with an
//     installed-but-not-started service, which is exactly what
//     setting StartAfterInstall=false also produces, so it's fine.
//  6. enable + start — final transition to a running service.
//
// We deliberately do NOT roll back on partial failure. Half-rolled-back
// installs are harder to diagnose than half-completed ones, and every
// step we perform is independently idempotent on the next `install`
// attempt. The contract is "an install error means: fix the cause,
// re-run install."
func (l *linuxManager) Install(opts Options) error {
	if !l.IsElevated() {
		return fmt.Errorf("%w: install requires root", ErrNotElevated)
	}
	opts = applyDefaults(opts)
	if !filepath.IsAbs(opts.BinaryPath) {
		return fmt.Errorf("%w: %q", ErrBinaryPathNotAbsolute, opts.BinaryPath)
	}

	// (1) ensure the source binary exists. CopyBinary=true wants the
	// running executable; CopyBinary=false wants the file already at
	// opts.BinaryPath.
	if opts.CopyBinary {
		selfPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve running executable: %w", err)
		}
		// Stat early so we fail before any host mutation.
		if _, err := os.Stat(selfPath); err != nil {
			return fmt.Errorf("stat running executable %q: %w", selfPath, err)
		}
	} else {
		if _, err := os.Stat(opts.BinaryPath); err != nil {
			return fmt.Errorf("stat %q (CopyBinary=false): %w", opts.BinaryPath, err)
		}
	}

	// (2) create the group, then add the invoking user to it so the
	// unprivileged hsh CLI / tray can reach the daemon without sudo.
	if opts.CreateGroup {
		if err := l.ensureGroup(opts.GroupName); err != nil {
			return fmt.Errorf("ensure group %q: %w", opts.GroupName, err)
		}
	}
	if opts.AddInvokingUser && opts.GroupName != "" {
		if err := l.addInvokingUserToGroup(opts.GroupName); err != nil {
			// Non-fatal: see service.go AddInvokingUser docs.
			fmt.Printf("hsh-tunneld: note: could not add invoking user to %q: %v\n", opts.GroupName, err)
		}
	}

	// (3) copy the binary if requested.
	if opts.CopyBinary {
		selfPath, _ := os.Executable() // already checked above
		if err := copyExecutable(selfPath, opts.BinaryPath); err != nil {
			return fmt.Errorf("copy executable to %q: %w", opts.BinaryPath, err)
		}
	}

	// (4) ensure config directory + empty config file exist. We
	// deliberately do NOT use ConfigurationDirectory= for the initial
	// creation; that clause only fires on service activation, but
	// validate-config runs before activation in ExecStartPre and would
	// fail if /etc/hsh/ does not exist yet. Creating it here makes
	// `sudo hsh-tunneld install` self-contained.
	if err := ensureConfigDirAndFile(opts.ConfigPath, opts.GroupName); err != nil {
		return fmt.Errorf("ensure config: %w", err)
	}

	// (5) write the unit file. We render to a string first so we can
	// detect "unit is identical to what's already on disk" and skip
	// the daemon-reload (which is a few hundred ms on busy hosts).
	body, err := renderUnit(opts)
	if err != nil {
		return err
	}
	changed, err := writeFileIfDifferent(l.unitPath(), []byte(body), 0644)
	if err != nil {
		return fmt.Errorf("write unit %q: %w", l.unitPath(), err)
	}
	if changed {
		if err := l.systemctl("daemon-reload"); err != nil {
			return fmt.Errorf("systemctl daemon-reload: %w", err)
		}
	}

	// (6) enable + start.
	if opts.EnableOnBoot {
		if err := l.systemctl("enable", l.unitName); err != nil {
			return fmt.Errorf("systemctl enable: %w", err)
		}
	}
	if opts.StartAfterInstall {
		if err := l.systemctl("restart", l.unitName); err != nil {
			return fmt.Errorf("systemctl restart: %w", err)
		}
	}
	return nil
}

// Uninstall reverses Install. It is tolerant of partial state — if
// only the unit was written but the service was never started, stop
// is a no-op and the disable + rm succeed normally.
func (l *linuxManager) Uninstall(opts PurgeOptions) error {
	if !l.IsElevated() {
		return fmt.Errorf("%w: uninstall requires root", ErrNotElevated)
	}
	if opts.BinaryPath == "" {
		opts.BinaryPath = PlatformDefaults().BinaryPath
	}
	if opts.ConfigPath == "" {
		opts.ConfigPath = PlatformDefaults().ConfigPath
	}
	if opts.GroupName == "" {
		opts.GroupName = PlatformDefaults().GroupName
	}

	// Stop + disable, tolerating "unit does not exist".
	_ = l.systemctl("stop", l.unitName)    // failing here is OK; unit may already be gone
	_ = l.systemctl("disable", l.unitName) // same

	// Remove the unit file and reload. If the file was already gone,
	// the daemon-reload is unnecessary but cheap.
	if err := os.Remove(l.unitPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove unit %q: %w", l.unitPath(), err)
	}
	_ = l.systemctl("daemon-reload")

	// Optional purges. We do these last so an `uninstall --purge`
	// that fails mid-way still leaves the service de-registered
	// (the most important user-visible state).
	if opts.RemoveBinary {
		if err := os.Remove(opts.BinaryPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove binary %q: %w", opts.BinaryPath, err)
		}
	}
	if opts.RemoveConfig {
		if err := os.RemoveAll(filepath.Dir(opts.ConfigPath)); err != nil {
			return fmt.Errorf("remove config dir %q: %w", filepath.Dir(opts.ConfigPath), err)
		}
	}
	if opts.RemoveGroup && opts.GroupName != "" {
		if err := l.removeGroup(opts.GroupName); err != nil {
			return fmt.Errorf("remove group %q: %w", opts.GroupName, err)
		}
	}
	return nil
}

// Status interrogates systemctl is-active / is-enabled to decide which
// of the three Status values applies. We tolerate "unit not found" as
// StatusNotInstalled and surface any other systemctl failure as an
// error — those usually mean systemd itself is unreachable, which is
// real news.
func (l *linuxManager) Status() (Status, error) {
	// 1. Does the unit exist? systemctl cat is the cheapest way to
	// check without parsing systemctl status output (which is
	// localised and prone to change).
	if _, err := os.Stat(l.unitPath()); errors.Is(err, os.ErrNotExist) {
		return StatusNotInstalled, nil
	} else if err != nil {
		return StatusNotInstalled, fmt.Errorf("stat unit: %w", err)
	}

	// 2. is-active. Exit 0 = running. Exit 3 = inactive/dead.
	// We don't treat exit 3 as an error.
	out, code, err := l.systemctlOutput("is-active", l.unitName)
	if err != nil && code == -1 {
		// Real exec failure (systemctl not on PATH, etc).
		return StatusNotInstalled, fmt.Errorf("systemctl is-active: %w", err)
	}
	switch strings.TrimSpace(out) {
	case "active":
		return StatusRunning, nil
	case "inactive", "failed", "activating", "deactivating":
		return StatusStopped, nil
	default:
		return StatusStopped, nil
	}
}

func (l *linuxManager) Start() error {
	if !l.IsElevated() {
		return fmt.Errorf("%w: start requires root", ErrNotElevated)
	}
	if _, err := os.Stat(l.unitPath()); errors.Is(err, os.ErrNotExist) {
		return ErrNotInstalled
	}
	return l.systemctl("start", l.unitName)
}

func (l *linuxManager) Stop() error {
	if !l.IsElevated() {
		return fmt.Errorf("%w: stop requires root", ErrNotElevated)
	}
	if _, err := os.Stat(l.unitPath()); errors.Is(err, os.ErrNotExist) {
		return ErrNotInstalled
	}
	return l.systemctl("stop", l.unitName)
}

// --- helpers below ---

// ensureGroup adds the requested group if it does not already exist.
// Idempotent: a pre-existing group is success.
func (l *linuxManager) ensureGroup(name string) error {
	if name == "" {
		return nil
	}
	if _, err := user.LookupGroup(name); err == nil {
		return nil // already exists
	}
	groupadd := l.groupaddPath
	if groupadd == "" {
		var err error
		groupadd, err = exec.LookPath("groupadd")
		if err != nil {
			return fmt.Errorf("groupadd not found on PATH: %w", err)
		}
	}
	// --system gives the group a low gid (< SYS_GID_MAX), which is
	// the convention for daemon-only groups. -f is "no error if it
	// already exists" so a TOCTOU race between LookupGroup and
	// groupadd does not blow up.
	cmd := exec.Command(groupadd, "--system", "-f", name)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("groupadd: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// addInvokingUserToGroup adds the human who ran `sudo hsh-tunneld
// install` (resolved from $SUDO_USER) to the named group via
// `usermod -aG`. Idempotent: a user already in the group is a no-op.
// Returns nil (not an error) when there is no invoking user — the
// real-root / packager case.
//
// The new membership only applies to new login sessions, so the caller
// must tell the user to relaunch any already-running shell/tray.
func (l *linuxManager) addInvokingUserToGroup(groupName string) error {
	username := invokingUser()
	if username == "" {
		return nil // real root login or packager hook — nothing to add
	}
	already, err := userInGroup(username, groupName)
	if err != nil {
		return err
	}
	if already {
		return nil
	}
	usermod := l.usermodPath
	if usermod == "" {
		usermod, err = exec.LookPath("usermod")
		if err != nil {
			return fmt.Errorf("usermod not found on PATH: %w", err)
		}
	}
	// -a appends (don't strip existing supplementary groups), -G names
	// the group. Order matters: `usermod -aG <grp> <user>`.
	cmd := exec.Command(usermod, "-aG", groupName, username)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("usermod -aG %s %s: %w (output: %s)", groupName, username, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// removeGroup deletes the group if it exists. Idempotent.
func (l *linuxManager) removeGroup(name string) error {
	if _, err := user.LookupGroup(name); err != nil {
		return nil // already gone
	}
	groupdel := l.groupdelPath
	if groupdel == "" {
		var err error
		groupdel, err = exec.LookPath("groupdel")
		if err != nil {
			return fmt.Errorf("groupdel not found on PATH: %w", err)
		}
	}
	cmd := exec.Command(groupdel, name)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("groupdel: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// systemctl runs `systemctl <args>` and returns any non-zero exit as
// an error with the captured output. Pure side-effect call; for
// commands whose stdout we care about (is-active, is-enabled) use
// systemctlOutput instead.
func (l *linuxManager) systemctl(args ...string) error {
	bin := l.systemctlPath
	if bin == "" {
		var err error
		bin, err = exec.LookPath("systemctl")
		if err != nil {
			return fmt.Errorf("systemctl not found: %w", err)
		}
	}
	cmd := exec.Command(bin, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl %s: %w (output: %s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// systemctlOutput is the version that returns stdout. It also returns
// the process's exit code so callers can branch on systemctl's
// documented non-zero-but-not-error returns (is-active returns 3 for
// inactive, etc).
func (l *linuxManager) systemctlOutput(args ...string) (string, int, error) {
	bin := l.systemctlPath
	if bin == "" {
		var err error
		bin, err = exec.LookPath("systemctl")
		if err != nil {
			return "", -1, fmt.Errorf("systemctl not found: %w", err)
		}
	}
	cmd := exec.Command(bin, args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return string(out), ee.ExitCode(), nil // not a Go error; let caller decide
		}
		return "", -1, err
	}
	return string(out), 0, nil
}
