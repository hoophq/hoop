//go:build darwin

package service

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestDarwinManager_Install_RequiresRoot asserts the elevation check
// short-circuits before any side effect. Skipped when the test happens
// to run as root so we never mutate /Library/LaunchDaemons on a
// developer box.
func TestDarwinManager_Install_RequiresRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; can't test the not-elevated branch")
	}
	m := &darwinManager{plistDir: t.TempDir(), label: launchdLabel}
	err := m.Install(Options{})
	if !errors.Is(err, ErrNotElevated) {
		t.Fatalf("expected ErrNotElevated, got: %v", err)
	}
}

// TestDarwinManager_Status_NoPlist returns NotInstalled with no error
// when the plist does not exist — Status short-circuits before touching
// launchctl.
func TestDarwinManager_Status_NoPlist(t *testing.T) {
	m := &darwinManager{plistDir: t.TempDir(), label: launchdLabel}
	st, err := m.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st != StatusNotInstalled {
		t.Fatalf("Status = %v, want StatusNotInstalled", st)
	}
}

// TestDarwinManager_PlistPathAndTarget pins the two derived strings the
// manager hands to launchctl. A drift between the plist filename and the
// service target's label component is the classic launchd footgun ("it
// writes the file but bootstrap can't find it").
func TestDarwinManager_PlistPathAndTarget(t *testing.T) {
	m := &darwinManager{plistDir: "/Library/LaunchDaemons", label: launchdLabel}
	wantPath := "/Library/LaunchDaemons/dev.hoop.hsh-tunneld.plist"
	if got := m.plistPath(); got != wantPath {
		t.Errorf("plistPath() = %q, want %q", got, wantPath)
	}
	wantTarget := "system/dev.hoop.hsh-tunneld"
	if got := m.target(); got != wantTarget {
		t.Errorf("target() = %q, want %q", got, wantTarget)
	}
}

// TestDarwinManager_PlatformName documents the human-readable identifier
// used in the install banner.
func TestDarwinManager_PlatformName(t *testing.T) {
	m := newPlatformManager()
	if m.PlatformName() != "launchd" {
		t.Errorf("PlatformName() = %q, want launchd", m.PlatformName())
	}
}

// TestIsAlreadyLoaded / TestIsNotLoaded pin the launchctl error-string
// classification. These substrings are how we keep Install/Stop
// idempotent across macOS versions; a typo would silently turn a
// tolerated no-op into a hard failure.
func TestIsAlreadyLoaded(t *testing.T) {
	yes := []string{
		"launchctl bootstrap: exit status 37 (output: Bootstrap failed: 37: ...)",
		"service already loaded",
		"Operation already in progress",
	}
	for _, m := range yes {
		if !isAlreadyLoaded(errors.New(m)) {
			t.Errorf("isAlreadyLoaded(%q) = false, want true", m)
		}
	}
	if isAlreadyLoaded(nil) {
		t.Error("isAlreadyLoaded(nil) = true")
	}
	if isAlreadyLoaded(errors.New("some unrelated failure")) {
		t.Error("isAlreadyLoaded(unrelated) = true")
	}
}

func TestIsNotLoaded(t *testing.T) {
	yes := []string{
		"launchctl bootout: Could not find specified service",
		"Boot-out failed: 3: No such process",
	}
	for _, m := range yes {
		if !isNotLoaded(errors.New(m)) {
			t.Errorf("isNotLoaded(%q) = false, want true", m)
		}
	}
	if isNotLoaded(nil) {
		t.Error("isNotLoaded(nil) = true")
	}
}

// TestDarwinManager_Install_WritesPlist drives Install end-to-end with a
// fake launchctl + dscl (recording scripts) so we exercise the real
// filesystem work (plist render + write, config dir creation) without a
// live launchd. We fake elevation by running the steps a real root
// install would, pointing every path at a temp dir.
//
// This mirrors the philosophy of the resolved package's fakeResolvectl:
// drive a real fork+exec of a recording script rather than monkey-patch
// os/exec, so argument quoting + the "binary on PATH" behaviour are
// exercised for real.
func TestDarwinManager_Install_WritesPlist(t *testing.T) {
	if os.Geteuid() != 0 {
		// Install's first act is the IsElevated() gate. We can't fake
		// geteuid, so the full end-to-end install can only run as root.
		// The render + write pieces are covered by the plist tests and
		// writeFileIfDifferent tests; here we only assert the elevation
		// gate is what blocks us, which the RequiresRoot test already
		// does. Skip the destructive path on non-root.
		t.Skip("Install end-to-end requires root; covered by render/write unit tests otherwise")
	}

	tmp := t.TempDir()
	plistDir := filepath.Join(tmp, "LaunchDaemons")
	if err := os.MkdirAll(plistDir, 0755); err != nil {
		t.Fatalf("mkdir plistDir: %v", err)
	}
	launchctl := writeRecordingScript(t, tmp, "launchctl", 0)
	dscl := writeRecordingScript(t, tmp, "dscl", 0)

	m := &darwinManager{
		plistDir:      plistDir,
		label:         launchdLabel,
		launchctlPath: launchctl,
		dsclPath:      dscl,
	}
	opts := DefaultOptions()
	opts.BinaryPath = filepath.Join(tmp, "hsh-tunneld")
	opts.ConfigPath = filepath.Join(tmp, "hsh", "config.toml")
	opts.SocketPath = filepath.Join(tmp, "hsh.sock")
	opts.CopyBinary = false // use the (touched) BinaryPath below
	opts.CreateGroup = false
	if err := os.WriteFile(opts.BinaryPath, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("seed binary: %v", err)
	}

	if err := m.Install(opts); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, err := os.Stat(m.plistPath()); err != nil {
		t.Errorf("plist not written: %v", err)
	}
}

// writeRecordingScript writes a /bin/sh script at <dir>/<name> that
// exits with the requested code, for use as a fake launchctl/dscl.
func writeRecordingScript(t *testing.T, dir, name string, exitCode int) string {
	t.Helper()
	p := filepath.Join(dir, name)
	body := "#!/bin/sh\nexit " + itoaSvc(exitCode) + "\n"
	if err := os.WriteFile(p, []byte(body), 0755); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

func itoaSvc(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
