//go:build darwin

package service

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"
)

// macOS LaunchDaemon backend for hsh-tunneld (RD-217).
//
// Design parallels the Linux/systemd manager (service_linux.go): all
// shelling out goes through a thin wrapper (launchctl / dscl), the
// idempotency contract documented in service.go is honoured step by
// step, and the POSIX filesystem helpers (copyExecutable,
// ensureConfigDirAndFile, writeFileIfDifferent) are shared via paths.go.
//
// launchctl API surface
//
// We use the modern (10.11+) launchctl subcommands, which operate on a
// service *target* of the form "<domain>/<label>":
//
//   - bootstrap <domain> <plist>   load + start a service from a plist
//   - bootout   <domain>/<label>   unload a service
//   - enable    <domain>/<label>   mark it to start at boot
//   - kickstart -k <target>        (re)start a loaded service
//   - print     <target>          dump state (used for Status)
//
// The domain for a system-wide LaunchDaemon is "system". We deliberately
// avoid the deprecated `launchctl load -w` / `unload` verbs: they are
// silently no-op on recent macOS for daemons that were bootstrapped, and
// mixing the two families is the usual cause of "it won't start and
// won't error" reports.
//
// Group creation
//
// macOS has no /etc/group-style groupadd. We create the `hsh` group via
// Directory Services (`dscl . -create /Groups/<name>` + a PrimaryGroupID
// in the system range). os/user.LookupGroup reads the same database, so
// the idempotency check is identical to Linux.

const (
	// launchdLabel is the reverse-DNS service label. It is both the
	// CFBundleIdentifier-style key inside the plist and the last path
	// component of the service target ("system/<label>").
	launchdLabel = "dev.hoop.hsh-tunneld"

	// launchdDomain is the launchd domain a system LaunchDaemon lives
	// in. Per-user agents would use "gui/<uid>"; we always run
	// system-wide because the daemon needs root for the utun device.
	launchdDomain = "system"

	// launchDaemonsDir is the canonical location for system-wide
	// LaunchDaemon plists. launchd scans it at boot.
	launchDaemonsDir = "/Library/LaunchDaemons"
)

// newPlatformManager returns the macOS/launchd implementation. Always
// non-nil; a missing launchctl at runtime is reported by the individual
// operations rather than at construction so unit tests can still
// introspect the manager.
func newPlatformManager() Manager {
	return &darwinManager{
		plistDir: launchDaemonsDir,
		label:    launchdLabel,
	}
}

// darwinManager is the launchd-backed Manager. Mirrors linuxManager.
type darwinManager struct {
	plistDir string // /Library/LaunchDaemons
	label    string // dev.hoop.hsh-tunneld

	// launchctlPath / dsclPath override exec.LookPath in tests.
	launchctlPath string
	dsclPath      string
}

func (d *darwinManager) PlatformName() string { return "launchd" }

func (d *darwinManager) IsElevated() bool { return os.Geteuid() == 0 }

// plistPath is the absolute path of the LaunchDaemon plist this manager
// owns.
func (d *darwinManager) plistPath() string {
	return filepath.Join(d.plistDir, d.label+".plist")
}

// target is the launchctl service target "<domain>/<label>".
func (d *darwinManager) target() string {
	return launchdDomain + "/" + d.label
}

// Install lays down the plist and (optionally) starts the daemon. The
// ordering and partial-failure philosophy match the Linux manager: every
// step is independently idempotent, and we do NOT roll back on a
// mid-install failure — fix the cause, re-run install.
//
//  1. Validate inputs (binary path absolute, source binary present).
//  2. Create the hsh group (idempotent).
//  3. Copy the binary to BinaryPath.
//  4. Ensure /etc/hsh/ + an empty config.toml exist for the
//     validate-config that the plist runs at launch.
//  5. Write the plist + (if changed) bootout-then-bootstrap so launchd
//     reloads it.
//  6. enable + kickstart.
func (d *darwinManager) Install(opts Options) error {
	if !d.IsElevated() {
		return fmt.Errorf("%w: install requires root", ErrNotElevated)
	}
	opts = applyDefaults(opts)
	if !filepath.IsAbs(opts.BinaryPath) {
		return fmt.Errorf("%w: %q", ErrBinaryPathNotAbsolute, opts.BinaryPath)
	}

	// (1) ensure the source binary exists.
	if opts.CopyBinary {
		selfPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve running executable: %w", err)
		}
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
		if err := d.ensureGroup(opts.GroupName); err != nil {
			return fmt.Errorf("ensure group %q: %w", opts.GroupName, err)
		}
	}
	if opts.AddInvokingUser && opts.GroupName != "" {
		if err := d.addInvokingUserToGroup(opts.GroupName); err != nil {
			// Non-fatal: a missing group membership is recoverable and
			// must not abort an otherwise-successful install. The CLI
			// surfaces a follow-up hint.
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

	// (4) ensure config directory + empty config file exist. launchd has
	// no ConfigurationDirectory= equivalent, so we always create it here;
	// the plist's validate-config ExecStartPre-equivalent (a wrapper, see
	// renderPlist) reads it at launch.
	if err := ensureConfigDirAndFile(opts.ConfigPath, opts.GroupName); err != nil {
		return fmt.Errorf("ensure config: %w", err)
	}

	// (5) write the plist. Render first so we can detect "identical to
	// what's on disk" and skip the reload.
	body, err := renderPlist(opts)
	if err != nil {
		return err
	}
	if _, err := writeFileIfDifferent(d.plistPath(), []byte(body), 0644); err != nil {
		return fmt.Errorf("write plist %q: %w", d.plistPath(), err)
	}
	// Reload deterministically: always bootout (so a reinstall with a
	// new binary actually picks it up) then bootstrap fresh. We do NOT
	// branch on whether the plist content changed — the service may be
	// loaded from an *older binary at the same path*, in which case the
	// plist is byte-identical but a reload is still required.
	//
	// Why not "bootstrap if not loaded, skip if loaded": on modern macOS,
	// `bootstrap` against an already-loaded service returns EIO (exit 5),
	// not a tidy EALREADY, and `bootout` is asynchronous — so a
	// bootout-then-immediate-bootstrap races the unload and also fails
	// with EIO. The robust pattern is bootout, wait until launchd reports
	// the service is actually gone, then bootstrap.
	if err := d.reloadService(); err != nil {
		return err
	}

	// (6) enable + start.
	if opts.EnableOnBoot {
		if err := d.launchctl("enable", d.target()); err != nil {
			return fmt.Errorf("launchctl enable: %w", err)
		}
	}
	if opts.StartAfterInstall {
		// kickstart -k restarts the service if already running, or
		// starts it if loaded-but-stopped — the launchd analogue of
		// `systemctl restart`.
		if err := d.launchctl("kickstart", "-k", d.target()); err != nil {
			return fmt.Errorf("launchctl kickstart: %w", err)
		}
	}
	return nil
}

// Uninstall reverses Install. Tolerant of partial state.
func (d *darwinManager) Uninstall(opts PurgeOptions) error {
	if !d.IsElevated() {
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

	// bootout unloads + stops the service. Tolerate "not loaded".
	_ = d.launchctl("bootout", d.target())

	// Remove the plist.
	if err := os.Remove(d.plistPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove plist %q: %w", d.plistPath(), err)
	}

	// Optional purges, last so a mid-purge failure still leaves the
	// service de-registered.
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
		if err := d.removeGroup(opts.GroupName); err != nil {
			return fmt.Errorf("remove group %q: %w", opts.GroupName, err)
		}
	}
	return nil
}

// Status maps the launchd service state onto our three-value Status.
//
//   - No plist on disk → StatusNotInstalled.
//   - Plist present but `launchctl print` reports the service is not
//     loaded → StatusStopped.
//   - Loaded and reporting a running/spawning pid → StatusRunning.
//   - Loaded but no pid (waiting / exited) → StatusStopped.
func (d *darwinManager) Status() (Status, error) {
	if _, err := os.Stat(d.plistPath()); errors.Is(err, os.ErrNotExist) {
		return StatusNotInstalled, nil
	} else if err != nil {
		return StatusNotInstalled, fmt.Errorf("stat plist: %w", err)
	}

	out, code, err := d.launchctlOutput("print", d.target())
	if err != nil && code == -1 {
		return StatusNotInstalled, fmt.Errorf("launchctl print: %w", err)
	}
	if code != 0 {
		// `launchctl print` exits non-zero (113, "Could not find service")
		// when the plist exists on disk but the service is not loaded.
		// That is "installed but stopped".
		return StatusStopped, nil
	}
	// Loaded. Look for a live pid in the print output. launchd prints
	// `pid = NNNN` only while the process is actually running.
	if strings.Contains(out, "pid = ") {
		return StatusRunning, nil
	}
	return StatusStopped, nil
}

func (d *darwinManager) Start() error {
	if !d.IsElevated() {
		return fmt.Errorf("%w: start requires root", ErrNotElevated)
	}
	if _, err := os.Stat(d.plistPath()); errors.Is(err, os.ErrNotExist) {
		return ErrNotInstalled
	}
	// If the service isn't loaded yet, bootstrap it; otherwise kickstart
	// the already-loaded one. We check load state first rather than
	// bootstrap-and-tolerate-error because bootstrap-on-loaded returns a
	// non-specific EIO on modern macOS (see reloadService).
	if !d.isLoaded() {
		if err := d.launchctl("bootstrap", launchdDomain, d.plistPath()); err != nil {
			return fmt.Errorf("launchctl bootstrap: %w", err)
		}
	}
	return d.launchctl("kickstart", d.target())
}

func (d *darwinManager) Stop() error {
	if !d.IsElevated() {
		return fmt.Errorf("%w: stop requires root", ErrNotElevated)
	}
	if _, err := os.Stat(d.plistPath()); errors.Is(err, os.ErrNotExist) {
		return ErrNotInstalled
	}
	// bootout unloads the service (the launchd way to stop a daemon).
	// Tolerate "not loaded" so a Stop on an already-stopped service is a
	// no-op per the contract.
	if err := d.launchctl("bootout", d.target()); err != nil {
		if isNotLoaded(err) {
			return nil
		}
		return fmt.Errorf("launchctl bootout: %w", err)
	}
	return nil
}

// --- helpers below ---

// ensureGroup creates the requested group via Directory Services if it
// does not already exist. Idempotent.
func (d *darwinManager) ensureGroup(name string) error {
	if name == "" {
		return nil
	}
	if _, err := user.LookupGroup(name); err == nil {
		return nil // already exists
	}
	dscl, err := d.dscl()
	if err != nil {
		return err
	}
	// Create the group record, then give it a PrimaryGroupID. macOS
	// daemon groups conventionally use gids in the 200–400 range; we pick
	// a stable, unlikely-to-collide value and let an existing-gid error
	// be surfaced (the operator can pick another via a packager hook).
	if err := runDSCL(dscl, ".", "-create", "/Groups/"+name); err != nil {
		return fmt.Errorf("dscl create group: %w", err)
	}
	gid, err := freeSystemGID()
	if err != nil {
		return err
	}
	if err := runDSCL(dscl, ".", "-create", "/Groups/"+name, "PrimaryGroupID", gid); err != nil {
		return fmt.Errorf("dscl set gid: %w", err)
	}
	return nil
}

// addInvokingUserToGroup adds the human who ran `sudo hsh-tunneld
// install` (resolved from $SUDO_USER) to the named group via Directory
// Services. Idempotent: a user already in the group is a no-op. Returns
// nil (not an error) when there is no invoking user to add — that is the
// real-root / packager case, which is a legitimate skip, not a failure.
//
// On macOS, supplementary group membership is recorded by appending the
// short user name to the group's GroupMembership attribute:
//
//	dscl . -append /Groups/<grp> GroupMembership <user>
//
// The new membership only applies to *new* login sessions, so the
// caller is responsible for telling the user that an already-running
// shell/tray must be relaunched to pick it up.
func (d *darwinManager) addInvokingUserToGroup(groupName string) error {
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
	dscl, err := d.dscl()
	if err != nil {
		return err
	}
	if err := runDSCL(dscl, ".", "-append", "/Groups/"+groupName, "GroupMembership", username); err != nil {
		return fmt.Errorf("dscl append membership: %w", err)
	}
	return nil
}

// removeGroup deletes the group via Directory Services if it exists.
// Idempotent.
func (d *darwinManager) removeGroup(name string) error {
	if _, err := user.LookupGroup(name); err != nil {
		return nil // already gone
	}
	dscl, err := d.dscl()
	if err != nil {
		return err
	}
	if err := runDSCL(dscl, ".", "-delete", "/Groups/"+name); err != nil {
		return fmt.Errorf("dscl delete group: %w", err)
	}
	return nil
}

// freeSystemGID returns a gid (as a string) not currently in use, in the
// macOS system-daemon range. We start at 300 and walk up until
// LookupGroupId reports the gid is unassigned. The range is small and the
// loop is bounded so we never spin.
func freeSystemGID() (string, error) {
	for gid := 300; gid < 500; gid++ {
		s := fmt.Sprintf("%d", gid)
		if _, err := user.LookupGroupId(s); err != nil {
			// LookupGroupId errors when the gid is unassigned — exactly
			// what we want.
			return s, nil
		}
	}
	return "", errors.New("no free gid in the 300-499 system range for the hsh group")
}

// launchctl runs `launchctl <args>` and returns any non-zero exit as an
// error with the captured output.
func (d *darwinManager) launchctl(args ...string) error {
	bin, err := d.launchctlBinary()
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl %s: %w (output: %s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// isLoaded reports whether launchd currently knows about the service.
// `launchctl print system/<label>` exits 0 when the service is loaded and
// non-zero (113, "Could not find service") when it is not. We use the
// load state as the source of truth rather than guessing from
// bootstrap/bootout exit codes, which are inconsistent across macOS
// versions (bootstrap-on-loaded returns EIO, not EALREADY).
func (d *darwinManager) isLoaded() bool {
	_, code, err := d.launchctlOutput("print", d.target())
	if err != nil {
		return false
	}
	return code == 0
}

// waitUntilNotLoaded polls until launchd reports the service is no longer
// loaded, or the timeout elapses. bootout is asynchronous — it returns
// before launchd has finished unloading — so a bootstrap issued
// immediately after a bootout races the teardown and fails with EIO.
// Waiting for the unload to complete removes that race.
//
// The timeout is generous (a few seconds) because a daemon with
// in-flight work can take a moment to exit after SIGTERM; the poll
// interval is short so the common fast case adds negligible latency.
func (d *darwinManager) waitUntilNotLoaded(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if !d.isLoaded() {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("service %s still loaded after %s", d.target(), timeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// reloadService performs an idempotent load of the service regardless of
// its current state: if loaded, bootout and wait for the unload to
// complete; then bootstrap fresh. This is the only reliable way to make
// `install` pick up a new binary at the same path (where the plist is
// byte-identical but the running process is stale) without tripping over
// launchd's EIO-on-already-loaded / async-bootout behaviour.
func (d *darwinManager) reloadService() error {
	if d.isLoaded() {
		// bootout is async; tolerate "not loaded" in case it raced away
		// between our check and the call.
		if err := d.launchctl("bootout", d.target()); err != nil && !isNotLoaded(err) {
			return fmt.Errorf("launchctl bootout (for reload): %w", err)
		}
		if err := d.waitUntilNotLoaded(5 * time.Second); err != nil {
			return fmt.Errorf("waiting for service to unload: %w", err)
		}
	}
	if err := d.launchctl("bootstrap", launchdDomain, d.plistPath()); err != nil {
		return fmt.Errorf("launchctl bootstrap: %w", err)
	}
	return nil
}

// launchctlOutput is the variant that returns stdout+stderr and the exit
// code so Status can branch on launchctl's documented non-zero returns.
func (d *darwinManager) launchctlOutput(args ...string) (string, int, error) {
	bin, err := d.launchctlBinary()
	if err != nil {
		return "", -1, err
	}
	cmd := exec.Command(bin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return string(out), ee.ExitCode(), nil
		}
		return "", -1, err
	}
	return string(out), 0, nil
}

func (d *darwinManager) launchctlBinary() (string, error) {
	if d.launchctlPath != "" {
		return d.launchctlPath, nil
	}
	bin, err := exec.LookPath("launchctl")
	if err != nil {
		return "", fmt.Errorf("launchctl not found: %w", err)
	}
	return bin, nil
}

func (d *darwinManager) dscl() (string, error) {
	if d.dsclPath != "" {
		return d.dsclPath, nil
	}
	bin, err := exec.LookPath("dscl")
	if err != nil {
		return "", fmt.Errorf("dscl not found: %w", err)
	}
	return bin, nil
}

func runDSCL(bin string, args ...string) error {
	cmd := exec.Command(bin, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("dscl %s: %w (output: %s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// isNotLoaded reports whether a bootout error means the service was not
// loaded to begin with (a no-op stop, which the contract treats as
// success).
func isNotLoaded(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "Could not find specified service") ||
		strings.Contains(msg, "No such process") ||
		strings.Contains(msg, "Boot-out failed: 3")
}
