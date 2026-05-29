//go:build !windows

package ipc

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
)

// TestFileTokenStore_ChownsToGroup verifies the control-token file is
// chowned to the configured IPC group, both on initial creation and
// after a Rotate. This is the fix for the bug where the token stayed
// owned by the daemon's primary group (root:daemon on macOS) and was
// therefore unreadable by members of the hsh group — making `hsh` need
// sudo despite the whole group-ACL design.
//
// We chown to the test user's *own primary group*: an unprivileged
// process can always chown a file it owns to a group it belongs to, so
// the test exercises the real chown path without needing root or
// mutating system group tables.
func TestFileTokenStore_ChownsToGroup(t *testing.T) {
	me, err := user.Current()
	if err != nil {
		t.Skipf("cannot resolve current user: %v", err)
	}
	grp, err := user.LookupGroupId(me.Gid)
	if err != nil {
		t.Skipf("cannot resolve current user's primary group: %v", err)
	}
	wantGID, err := strconv.Atoi(me.Gid)
	if err != nil {
		t.Fatalf("parse gid: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "control-token")

	store, err := NewFileTokenStore(path, FileTokenOptions{
		Mode:      0o640,
		DirMode:   0o750,
		GroupName: grp.Name,
	})
	if err != nil {
		t.Fatalf("NewFileTokenStore: %v", err)
	}

	assertGID := func(stage string) {
		t.Helper()
		var st syscall.Stat_t
		if err := syscall.Stat(path, &st); err != nil {
			t.Fatalf("%s: stat: %v", stage, err)
		}
		if int(st.Gid) != wantGID {
			t.Errorf("%s: token file gid = %d, want %d", stage, st.Gid, wantGID)
		}
	}

	// Group must be correct on the freshly-created (empty) file...
	assertGID("after create")

	// ...and preserved across a rotation (which writes a temp file +
	// renames it into place).
	if _, err := store.Rotate(); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	assertGID("after rotate")
}

// TestFileTokenStore_UnknownGroupErrors ensures a misconfigured group
// name fails loudly at construction rather than silently leaving the
// token at the wrong group (which would manifest much later as a
// confusing "token not readable" on the client).
func TestFileTokenStore_UnknownGroupErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "control-token")
	_, err := NewFileTokenStore(path, FileTokenOptions{
		GroupName: "this-group-does-not-exist-hsh-test",
	})
	if err == nil {
		t.Fatal("expected error for unknown group, got nil")
	}
	if _, statErr := os.Stat(path); statErr == nil {
		// We failed before truncating/creating — acceptable either way,
		// but if the file exists it must not be left half-configured.
		t.Log("note: token file was created before the group error; that is fine")
	}
}
