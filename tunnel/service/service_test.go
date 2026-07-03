package service

import (
	"errors"
	"runtime"
	"strings"
	"testing"
)

// TestPlatformDefaults_Sane covers the platform-default options. The
// install logic relies on these being absolute, non-empty paths for
// every supported GOOS; a regression here (someone setting BinaryPath
// to an empty string by accident) would silently break the systemd
// unit file, which would then take a real install to find.
func TestPlatformDefaults_Sane(t *testing.T) {
	got := PlatformDefaults()
	// We assert against the *current* GOOS values: the function picks
	// between linux/darwin/windows/other at runtime. Tests run on the
	// developer's host, so on linux/darwin the BinaryPath is the
	// /usr/local/bin one, and on every supported GOOS GroupName and
	// SocketPath are non-empty.
	switch {
	case got.BinaryPath == "":
		t.Fatal("BinaryPath empty")
	case got.ConfigPath == "":
		t.Fatal("ConfigPath empty")
	case got.SocketPath == "":
		t.Fatal("SocketPath empty")
	case !strings.HasPrefix(got.BinaryPath, "/") && !strings.HasPrefix(got.BinaryPath, "C:") && got.BinaryPath != "":
		t.Fatalf("BinaryPath %q is not absolute", got.BinaryPath)
	}
}

// TestApplyDefaults_OverlaysOnlyEmpty asserts that explicit fields win
// and zero-value fields get filled from PlatformDefaults. The
// CopyBinary / CreateGroup / EnableOnBoot / StartAfterInstall flags are
// not touched here because they're booleans the constructor wires
// directly — see DefaultOptions for that path.
func TestApplyDefaults_OverlaysOnlyEmpty(t *testing.T) {
	in := Options{
		BinaryPath: "/opt/custom/hsh-tunneld",
		GroupName:  "wheel",
	}
	out := applyDefaults(in)
	if out.BinaryPath != "/opt/custom/hsh-tunneld" {
		t.Errorf("BinaryPath was overwritten: %q", out.BinaryPath)
	}
	if out.GroupName != "wheel" {
		t.Errorf("GroupName was overwritten: %q", out.GroupName)
	}
	if out.ConfigPath == "" {
		t.Error("ConfigPath was not defaulted")
	}
	if out.SocketPath == "" {
		t.Error("SocketPath was not defaulted")
	}
}

// TestStatus_String covers the human-readable rendering used in the
// CLI banner. Worth pinning because the strings appear in the
// install/uninstall summary lines and any change is user-visible.
func TestStatus_String(t *testing.T) {
	cases := []struct {
		s    Status
		want string
	}{
		{StatusNotInstalled, "not_installed"},
		{StatusStopped, "stopped"},
		{StatusRunning, "running"},
		{Status(99), "unknown"},
	}
	for _, c := range cases {
		if got := c.s.String(); got != c.want {
			t.Errorf("Status(%d).String() = %q, want %q", c.s, got, c.want)
		}
	}
}

// TestDefaultOptions_AddInvokingUser pins the default that makes the
// post-install UX seamless: on POSIX the installing user is auto-added
// to the hsh group; on Windows (DACL model, no group) it stays off.
func TestDefaultOptions_AddInvokingUser(t *testing.T) {
	got := DefaultOptions()
	switch runtime.GOOS {
	case "linux", "darwin":
		if !got.AddInvokingUser {
			t.Error("AddInvokingUser should default true on linux/darwin")
		}
		if got.GroupName == "" {
			t.Error("GroupName should be non-empty when AddInvokingUser is on")
		}
	case "windows":
		if got.AddInvokingUser {
			t.Error("AddInvokingUser should default false on windows")
		}
	}
}

// TestNew_ReturnsManager makes sure New always returns a usable value
// regardless of GOOS — the stub paths must not return nil.
func TestNew_ReturnsManager(t *testing.T) {
	m := New()
	if m == nil {
		t.Fatal("New returned nil")
	}
	if m.PlatformName() == "" {
		t.Fatal("PlatformName empty")
	}
}

// TestDefaultPurgeOptions_DefaultsAreSafe asserts that the
// "everything off" defaults match the documented contract — purging
// user state must be explicit.
func TestDefaultPurgeOptions_DefaultsAreSafe(t *testing.T) {
	got := DefaultPurgeOptions()
	if got.RemoveConfig {
		t.Error("RemoveConfig should default to false")
	}
	if got.RemoveBinary {
		t.Error("RemoveBinary should default to false")
	}
	if got.RemoveGroup {
		t.Error("RemoveGroup should default to false")
	}
}

// TestUnsupportedPlatformErrorWraps verifies that the sentinel error
// returned by stubs is identifiable via errors.Is, which is how the
// CLI verb checks for "this platform isn't ready yet".
func TestUnsupportedPlatformErrorWraps(t *testing.T) {
	// "windows" and "unsupported" are the platforms still backed by the
	// stub (Linux and macOS now have real managers). We construct the
	// stub directly so the test is independent of the host GOOS.
	s := &stubManager{platform: "windows"}
	err := s.Install(Options{})
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("expected wrapped ErrUnsupportedPlatform, got: %v", err)
	}
	if !strings.Contains(err.Error(), "windows") {
		t.Errorf("error did not include platform name: %v", err)
	}
}
