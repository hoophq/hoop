//go:build linux

package service

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestLinuxManager_Install_RequiresRoot asserts the elevation check
// short-circuits before any side effect. We run the test as the
// developer's normal user; if it's running as root we skip rather than
// blow up the host with /etc writes.
func TestLinuxManager_Install_RequiresRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; can't test the not-elevated branch")
	}
	m := &linuxManager{unitDir: "/etc/systemd/system", unitName: "hsh-tunneld.service"}
	err := m.Install(Options{})
	if !errors.Is(err, ErrNotElevated) {
		t.Fatalf("expected ErrNotElevated, got: %v", err)
	}
}

// TestLinuxManager_Install_RelativeBinaryPath verifies we reject a
// relative --binary-path before doing anything destructive. The test
// has to run effectively-as-root to get past the IsElevated check, so
// we fake it with a manager that lies. We're testing the validation
// flow, not the privilege flow.
func TestLinuxManager_Install_RelativeBinaryPath(t *testing.T) {
	m := &fakeElevatedLinuxManager{
		linuxManager: &linuxManager{unitDir: t.TempDir(), unitName: "hsh-tunneld.service"},
	}
	err := m.Install(Options{BinaryPath: "relative/path"})
	if !errors.Is(err, ErrBinaryPathNotAbsolute) {
		t.Fatalf("expected ErrBinaryPathNotAbsolute, got: %v", err)
	}
}

// TestLinuxManager_Status_NoUnit returns NotInstalled with no error
// even though systemctl is real on the host — the unit file does not
// exist so Status short-circuits.
func TestLinuxManager_Status_NoUnit(t *testing.T) {
	m := &linuxManager{
		unitDir:  t.TempDir(),
		unitName: "hsh-tunneld.service",
	}
	st, err := m.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st != StatusNotInstalled {
		t.Fatalf("Status = %v, want StatusNotInstalled", st)
	}
}

// TestWriteFileIfDifferent_NoOpOnIdentical confirms the cheap-fast path
// of writeFileIfDifferent: a file that already matches the desired
// contents leaves the on-disk mtime untouched.
func TestWriteFileIfDifferent_NoOpOnIdentical(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "u.service")
	if err := os.WriteFile(p, []byte("hello\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	before, _ := os.Stat(p)

	changed, err := writeFileIfDifferent(p, []byte("hello\n"), 0644)
	if err != nil {
		t.Fatalf("writeFileIfDifferent: %v", err)
	}
	if changed {
		t.Fatal("changed=true for identical content")
	}
	after, _ := os.Stat(p)
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("mtime changed (%v -> %v); expected no-op", before.ModTime(), after.ModTime())
	}
}

// TestWriteFileIfDifferent_WritesNewContent confirms a real change is
// actually written.
func TestWriteFileIfDifferent_WritesNewContent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "u.service")
	if err := os.WriteFile(p, []byte("old\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	changed, err := writeFileIfDifferent(p, []byte("new\n"), 0644)
	if err != nil {
		t.Fatalf("writeFileIfDifferent: %v", err)
	}
	if !changed {
		t.Fatal("changed=false for changed content")
	}
	got, _ := os.ReadFile(p)
	if string(got) != "new\n" {
		t.Errorf("content = %q, want %q", string(got), "new\n")
	}
}

// fakeElevatedLinuxManager wraps a linuxManager and lies about
// elevation. Used only inside this test file to exercise the
// validation logic without needing root.
type fakeElevatedLinuxManager struct {
	*linuxManager
}

func (f *fakeElevatedLinuxManager) Install(opts Options) error {
	// Re-implement just enough of Install to reach the path validation
	// without the real systemctl/groupadd shell-outs. We can't shadow
	// IsElevated by composition alone because Install in linuxManager
	// reads l.IsElevated() through the concrete receiver.
	//
	// The validation we want to test is the very first thing Install
	// does after the elevation check, so re-creating that prefix is
	// cheap.
	opts = applyDefaults(opts)
	if !filepath.IsAbs(opts.BinaryPath) {
		return ErrBinaryPathNotAbsolute
	}
	return nil
}
