//go:build linux || darwin

package service

import (
	"os"
	"os/user"
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

// TestInvokingUser covers the $SUDO_USER resolution that drives the
// add-to-group step. A real-root login (SUDO_USER unset or "root") must
// resolve to "" so Install skips the membership add rather than trying
// to add root to the hsh group.
func TestInvokingUser(t *testing.T) {
	cases := []struct {
		set   bool
		value string
		want  string
	}{
		{true, "alice", "alice"},
		{true, "root", ""}, // sudo -i / sudo su — not a real target user
		{true, "", ""},     // explicitly empty
		{false, "", ""},    // unset entirely (real root login)
	}
	for _, c := range cases {
		if c.set {
			t.Setenv("SUDO_USER", c.value)
		} else {
			// t.Setenv can't unset; emulate "unset" by clearing it.
			t.Setenv("SUDO_USER", "")
		}
		if got := invokingUser(); got != c.want {
			t.Errorf("invokingUser() with SUDO_USER=%q (set=%v) = %q, want %q", c.value, c.set, got, c.want)
		}
	}
}

// TestUserInGroup_SelfPrimaryGroup sanity-checks the membership probe
// against the running test user's own primary group, which they are by
// definition a member of. Keeps the idempotency check honest without
// needing root to mutate group state.
func TestUserInGroup_SelfPrimaryGroup(t *testing.T) {
	me, err := user.Current()
	if err != nil {
		t.Skipf("cannot resolve current user: %v", err)
	}
	grp, err := user.LookupGroupId(me.Gid)
	if err != nil {
		t.Skipf("cannot resolve current user's primary group: %v", err)
	}
	in, err := userInGroup(me.Username, grp.Name)
	if err != nil {
		t.Fatalf("userInGroup: %v", err)
	}
	if !in {
		t.Errorf("userInGroup(%q, %q) = false; the user must be in their own primary group", me.Username, grp.Name)
	}
}

// TestUserInGroup_NonMember verifies a user is reported as not-a-member
// of a group they don't belong to. We use a group the test user is very
// unlikely to be in; if the lookup fails (group absent) we skip rather
// than assert, since group tables vary across CI images.
func TestUserInGroup_NonMember(t *testing.T) {
	me, err := user.Current()
	if err != nil {
		t.Skipf("cannot resolve current user: %v", err)
	}
	// "daemon" exists on virtually every POSIX system and the test user
	// is essentially never a member of it.
	if _, err := user.LookupGroup("daemon"); err != nil {
		t.Skip("no 'daemon' group on this host")
	}
	in, err := userInGroup(me.Username, "daemon")
	if err != nil {
		t.Fatalf("userInGroup: %v", err)
	}
	if in {
		t.Skip("test user is unexpectedly in the 'daemon' group; skipping negative assertion")
	}
}
