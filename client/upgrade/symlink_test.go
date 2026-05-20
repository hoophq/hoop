//go:build !windows

package upgrade

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSetActiveCreatesAndRetargets(t *testing.T) {
	home := t.TempDir()
	layout := LayoutFromHome(home)
	if err := layout.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	installVersion(t, layout, "1.0.0")
	installVersion(t, layout, "1.1.0")
	store := &Store{
		Versions: []VersionEntry{{Version: "1.0.0"}, {Version: "1.1.0"}},
	}

	if err := SetActive(layout, store, "1.0.0"); err != nil {
		t.Fatalf("SetActive 1.0.0: %v", err)
	}
	if store.Active != "1.0.0" {
		t.Fatalf("Active not updated: %s", store.Active)
	}
	got, err := os.Readlink(layout.BinLink)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if got != layout.VersionBinary("1.0.0") {
		t.Fatalf("symlink target=%s want %s", got, layout.VersionBinary("1.0.0"))
	}

	if err := SetActive(layout, store, "1.1.0"); err != nil {
		t.Fatalf("SetActive 1.1.0: %v", err)
	}
	got, _ = os.Readlink(layout.BinLink)
	if got != layout.VersionBinary("1.1.0") {
		t.Fatalf("re-target failed: %s", got)
	}
}

func TestSetActiveRefusesUserManagedFile(t *testing.T) {
	home := t.TempDir()
	layout := LayoutFromHome(home)
	if err := layout.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	installVersion(t, layout, "1.0.0")
	store := &Store{Versions: []VersionEntry{{Version: "1.0.0"}}}

	if err := os.WriteFile(layout.BinLink, []byte("real binary"), 0755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	err := SetActive(layout, store, "1.0.0")
	if err == nil {
		t.Fatalf("expected ErrBinLinkConflict")
	}
	if !errors.Is(err, ErrBinLinkConflict) {
		t.Fatalf("expected ErrBinLinkConflict, got %v", err)
	}
}

func TestSetActiveRefusesSymlinkOutsideVersionsDir(t *testing.T) {
	home := t.TempDir()
	layout := LayoutFromHome(home)
	if err := layout.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	installVersion(t, layout, "1.0.0")
	store := &Store{Versions: []VersionEntry{{Version: "1.0.0"}}}

	external := filepath.Join(home, "external-hoop")
	if err := os.WriteFile(external, []byte("external"), 0755); err != nil {
		t.Fatalf("write external: %v", err)
	}
	if err := os.Symlink(external, layout.BinLink); err != nil {
		t.Fatalf("create external symlink: %v", err)
	}

	err := SetActive(layout, store, "1.0.0")
	if !errors.Is(err, ErrBinLinkConflict) {
		t.Fatalf("expected ErrBinLinkConflict, got %v", err)
	}
}

func TestSetActiveErrorsWhenNotInStore(t *testing.T) {
	home := t.TempDir()
	layout := LayoutFromHome(home)
	_ = layout.EnsureDirs()
	installVersion(t, layout, "1.0.0")
	store := &Store{}
	if err := SetActive(layout, store, "1.0.0"); err == nil {
		t.Fatalf("expected error when version missing from store")
	}
}

func installVersion(t *testing.T, layout Layout, version string) {
	t.Helper()
	if err := os.MkdirAll(layout.VersionDir(version), 0700); err != nil {
		t.Fatalf("mkdir version: %v", err)
	}
	if err := os.WriteFile(layout.VersionBinary(version), []byte("hoop-"+version), 0755); err != nil {
		t.Fatalf("write binary: %v", err)
	}
}
