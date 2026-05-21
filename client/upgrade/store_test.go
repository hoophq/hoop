package upgrade

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreRoundTrip(t *testing.T) {
	layout := LayoutFromHome(t.TempDir())
	if err := layout.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	in := &Store{
		Active: "1.73.0",
		Versions: []VersionEntry{
			{
				Version:     "1.72.0",
				InstalledAt: time.Date(2026, 5, 19, 14, 10, 0, 0, time.UTC),
				Platform:    "Darwin_arm64",
				SHA256:      "3874f853c7ecc9510002f018c99d473b5f30a944d3552b709815d16fcdd970b3",
				SourceURL:   "https://releases.hoop.dev/release/1.72.0/hoop_1.72.0_Darwin_arm64.tar.gz",
			},
			{
				Version:     "1.73.0",
				InstalledAt: time.Date(2026, 5, 19, 14, 25, 0, 0, time.UTC),
				Platform:    "Darwin_arm64",
				SHA256:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				SourceURL:   "https://releases.hoop.dev/release/1.73.0/hoop_1.73.0_Darwin_arm64.tar.gz",
			},
		},
	}
	if err := in.Save(layout); err != nil {
		t.Fatalf("Save: %v", err)
	}

	out, err := LoadStore(layout)
	if err != nil {
		t.Fatalf("LoadStore: %v", err)
	}
	if out.Active != in.Active {
		t.Fatalf("Active: want %s got %s", in.Active, out.Active)
	}
	if len(out.Versions) != len(in.Versions) {
		t.Fatalf("Versions count mismatch: want %d got %d", len(in.Versions), len(out.Versions))
	}
	for i, e := range out.Versions {
		if !e.InstalledAt.Equal(in.Versions[i].InstalledAt) {
			t.Fatalf("InstalledAt[%d]: want %v got %v", i, in.Versions[i].InstalledAt, e.InstalledAt)
		}
		if e.Version != in.Versions[i].Version {
			t.Fatalf("Version[%d]: %s vs %s", i, e.Version, in.Versions[i].Version)
		}
		if e.SHA256 != in.Versions[i].SHA256 {
			t.Fatalf("SHA256[%d]", i)
		}
	}
}

func TestStoreMissingFile(t *testing.T) {
	layout := LayoutFromHome(t.TempDir())
	out, err := LoadStore(layout)
	if err != nil {
		t.Fatalf("LoadStore on missing file: %v", err)
	}
	if len(out.Versions) != 0 || out.Active != "" {
		t.Fatalf("expected empty store, got %+v", out)
	}
}

func TestStoreUpsertAndRemove(t *testing.T) {
	s := &Store{}
	s.Upsert(VersionEntry{Version: "1.0.0", SHA256: "a"})
	s.Upsert(VersionEntry{Version: "1.1.0", SHA256: "b"})
	s.Upsert(VersionEntry{Version: "1.0.0", SHA256: "c"})

	if len(s.Versions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(s.Versions))
	}
	got, ok := s.Get("1.0.0")
	if !ok || got.SHA256 != "c" {
		t.Fatalf("upsert should have replaced 1.0.0: %+v", got)
	}

	s.Active = "1.0.0"
	if !s.Remove("1.0.0") {
		t.Fatalf("Remove returned false")
	}
	if s.Active != "" {
		t.Fatalf("Active should be cleared when active version removed: %s", s.Active)
	}
	if len(s.Versions) != 1 {
		t.Fatalf("expected 1 version after remove")
	}
}

func TestStoreSavePermsAndAtomicity(t *testing.T) {
	home := t.TempDir()
	layout := LayoutFromHome(home)
	s := &Store{Active: "1.0.0", Versions: []VersionEntry{{Version: "1.0.0"}}}
	if err := s.Save(layout); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(layout.StoreFile)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("expected 0600 perms, got %v", perm)
	}
	entries, err := os.ReadDir(layout.Home)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("temp file leaked: %s", e.Name())
		}
	}
}
