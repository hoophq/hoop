package upgrade

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeVersionBinary(t *testing.T, l Layout, version string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(l.VersionDir(version), 0700); err != nil {
		t.Fatalf("mkdir version dir: %v", err)
	}
	if err := os.WriteFile(l.VersionBinary(version), content, 0755); err != nil {
		t.Fatalf("write version binary: %v", err)
	}
}

func TestCopyActivateFreshAndRetarget(t *testing.T) {
	home := t.TempDir()
	l := LayoutFromHome(home)
	if err := l.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	v1 := []byte("hoop-1.0.0-bytes")
	v2 := []byte("hoop-1.1.0-different-bytes")
	writeVersionBinary(t, l, "1.0.0", v1)
	writeVersionBinary(t, l, "1.1.0", v2)
	store := &Store{Versions: []VersionEntry{{Version: "1.0.0"}, {Version: "1.1.0"}}}

	// Fresh activation: bin path doesn't exist yet.
	if err := copyActivate(l, store, l.VersionBinary("1.0.0")); err != nil {
		t.Fatalf("copyActivate 1.0.0: %v", err)
	}
	if got, _ := os.ReadFile(l.BinLink); !bytes.Equal(got, v1) {
		t.Fatalf("bin path is not a copy of 1.0.0: %q", got)
	}

	// Retarget: the existing bin path is a copy we made (owned), so the
	// switch is allowed and the bytes change to 1.1.0.
	if err := copyActivate(l, store, l.VersionBinary("1.1.0")); err != nil {
		t.Fatalf("copyActivate 1.1.0: %v", err)
	}
	if got, _ := os.ReadFile(l.BinLink); !bytes.Equal(got, v2) {
		t.Fatalf("bin path is not a copy of 1.1.0: %q", got)
	}

	// No staging or retired leftovers should remain after a clean run.
	leftovers, _ := filepath.Glob(filepath.Join(l.BinDir, binaryName+".*"))
	if len(leftovers) != 0 {
		t.Fatalf("unexpected leftovers in bin dir: %v", leftovers)
	}
}

func TestCopyActivateRefusesForeignFile(t *testing.T) {
	home := t.TempDir()
	l := LayoutFromHome(home)
	if err := l.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	writeVersionBinary(t, l, "1.0.0", []byte("hoop-1.0.0-bytes"))
	store := &Store{Versions: []VersionEntry{{Version: "1.0.0"}}}

	// A file the version manager didn't create (content matches no version).
	if err := os.WriteFile(l.BinLink, []byte("a user's own binary"), 0755); err != nil {
		t.Fatalf("write foreign file: %v", err)
	}

	err := copyActivate(l, store, l.VersionBinary("1.0.0"))
	if !errors.Is(err, ErrBinLinkConflict) {
		t.Fatalf("expected ErrBinLinkConflict, got %v", err)
	}
	// The foreign file must be left untouched.
	if got, _ := os.ReadFile(l.BinLink); !bytes.Equal(got, []byte("a user's own binary")) {
		t.Fatalf("foreign file was modified: %q", got)
	}
}

func TestAssertOwnedCopyAllowsMissingBin(t *testing.T) {
	l := LayoutFromHome(t.TempDir())
	if err := l.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	if err := assertOwnedCopy(l, &Store{}, l.BinLink); err != nil {
		t.Fatalf("assertOwnedCopy on missing bin should be nil, got %v", err)
	}
}
