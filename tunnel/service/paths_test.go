//go:build linux || darwin

package service

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWriteFileIfDifferent_NoOpOnIdentical confirms the cheap-fast path
// of writeFileIfDifferent: a file that already matches the desired
// contents leaves the on-disk mtime untouched. Shared between the Linux
// (systemd unit) and macOS (launchd plist) backends, so it lives in the
// shared (linux||darwin) test file alongside the helper.
func TestWriteFileIfDifferent_NoOpOnIdentical(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "u.cfg")
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
	p := filepath.Join(dir, "u.cfg")
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

// TestEnsureConfigDirAndFile_CreatesAndPreserves verifies the shared
// config-dir helper creates the directory + empty config on a fresh
// install and leaves an existing config untouched on reinstall.
func TestEnsureConfigDirAndFile_CreatesAndPreserves(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "hsh", "config.toml")

	// Fresh: creates dir + file. GroupName "" skips the chown branch so
	// the test does not need root.
	if err := ensureConfigDirAndFile(cfgPath, ""); err != nil {
		t.Fatalf("ensureConfigDirAndFile (fresh): %v", err)
	}
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("config file not created: %v", err)
	}

	// Seed a token and re-run: existing file must be preserved.
	const sentinel = "token=\"preserve-me\"\n"
	if err := os.WriteFile(cfgPath, []byte(sentinel), 0600); err != nil {
		t.Fatalf("seed token: %v", err)
	}
	if err := ensureConfigDirAndFile(cfgPath, ""); err != nil {
		t.Fatalf("ensureConfigDirAndFile (reinstall): %v", err)
	}
	got, _ := os.ReadFile(cfgPath)
	if string(got) != sentinel {
		t.Errorf("reinstall clobbered existing config: got %q", string(got))
	}
}
