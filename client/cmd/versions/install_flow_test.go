package versions

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/hoophq/hoop/client/upgrade"
)

// installedLayout returns a layout under a temp home where version is
// already installed, so Install needs no download.
func installedLayout(t *testing.T, version string) upgrade.Layout {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("activation uses a symlink on unix only")
	}
	layout := upgrade.LayoutFromHome(t.TempDir())
	if err := os.MkdirAll(layout.VersionDir(version), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.VersionBinary(version), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	store, err := upgrade.LoadStore(layout)
	if err != nil {
		t.Fatal(err)
	}
	store.Upsert(upgrade.VersionEntry{Version: version, InstalledAt: time.Now()})
	if err := store.Save(layout); err != nil {
		t.Fatal(err)
	}
	return layout
}

func TestInstallAndActivateInRefusedKeepsActive(t *testing.T) {
	skipPathHint = true
	t.Cleanup(func() { skipPathHint = false })
	layout := installedLayout(t, "1.286.0")
	var gotBinary string
	refuse := errors.New("no context command")
	err := installAndActivateIn(layout, "1.286.0", func(binary string) error {
		gotBinary = binary
		return refuse
	})
	var notActivated *NotActivatedError
	if !errors.As(err, &notActivated) || notActivated.Version != "1.286.0" || !errors.Is(err, refuse) {
		t.Fatalf("err = %v, want *NotActivatedError for 1.286.0", err)
	}
	if gotBinary != layout.VersionBinary("1.286.0") {
		t.Fatalf("accept got %q", gotBinary)
	}
	store, err := upgrade.LoadStore(layout)
	if err != nil {
		t.Fatal(err)
	}
	if store.Active != "" {
		t.Fatalf("active = %q, want unchanged", store.Active)
	}
	if _, err := os.Lstat(layout.BinLink); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("bin link exists after a refused activation: %v", err)
	}
	// still installed, so `hoop versions use` works
	if !store.Has("1.286.0") {
		t.Fatal("refused version is not in the store")
	}
}

func TestInstallAndActivateInAccepted(t *testing.T) {
	skipPathHint = true
	t.Cleanup(func() { skipPathHint = false })
	layout := installedLayout(t, "1.286.0")
	if err := installAndActivateIn(layout, "1.286.0", func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	store, err := upgrade.LoadStore(layout)
	if err != nil {
		t.Fatal(err)
	}
	if store.Active != "1.286.0" {
		t.Fatalf("active = %q, want 1.286.0", store.Active)
	}
	if target, err := os.Readlink(layout.BinLink); err != nil || filepath.Clean(target) != layout.VersionBinary("1.286.0") {
		t.Fatalf("bin link = %q, %v", target, err)
	}
}
