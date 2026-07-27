package appconfig

import (
	"os"
	"path/filepath"
	"testing"
)

// MIGRATION_PATH_FILES semantics: unset means the migrations embedded in
// the binary are used; when set, the directory must contain the first
// migration file (fail-fast against typos and stale mounts) and trailing
// slashes are normalized.
func TestLoadMigrationPathFiles(t *testing.T) {
	validDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(validDir, "000001_init.up.sql"), []byte("SELECT 1;"), 0o600); err != nil {
		t.Fatal(err)
	}
	emptyDir := t.TempDir()

	for _, tc := range []struct {
		name     string
		env      string
		wantErr  bool
		wantPath string
	}{
		{"unset means embedded", "", false, ""},
		{"valid directory", validDir, false, validDir},
		{"trailing slash normalized", validDir + "/", false, validDir},
		{"missing first migration fails fast", emptyDir, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("POSTGRES_DB_URI", "postgres://u:p@localhost:5432/db")
			t.Setenv("MIGRATION_PATH_FILES", tc.env)
			runtimeConfig = Config{}

			err := Load()
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected Load to fail, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Load failed: %v", err)
			}
			if got := Get().MigrationPathFiles(); got != tc.wantPath {
				t.Fatalf("MigrationPathFiles() = %q, want %q", got, tc.wantPath)
			}
		})
	}
}
