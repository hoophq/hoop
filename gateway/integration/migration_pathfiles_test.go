//go:build integration

package integration

import (
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	migrationfiles "github.com/hoophq/hoop/gateway/migrations"
	modelsbootstrap "github.com/hoophq/hoop/gateway/models/bootstrap"
	_ "github.com/lib/pq"
)

// TestMigrateDBFromDiskPath covers the MIGRATION_PATH_FILES override:
// deployments that manage migration files externally load them from a
// directory on disk instead of the copy embedded in the binary. Both
// real-world layouts are exercised: the gateway bundle and the AWS
// deployment ship only the *.up.sql files (/opt/hoop/migrations), while the
// container images ship the full up+down set (/app/migrations).
func TestMigrateDBFromDiskPath(t *testing.T) {
	if testGateway.Postgres == nil {
		t.Skip("disk-path migrations require the PostgreSQL container backend")
	}

	for _, tc := range []struct {
		name    string
		include func(name string) bool
	}{
		{"bundle layout up-only", func(name string) bool { return strings.HasSuffix(name, ".up.sql") }},
		{"image layout full set", func(name string) bool { return strings.HasSuffix(name, ".sql") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			migrationDir := stageEmbeddedMigrations(t, tc.include)
			freshURI := createFreshDatabase(t)

			if err := modelsbootstrap.MigrateDB(freshURI, migrationDir); err != nil {
				t.Fatalf("MigrateDB from disk path failed: %v", err)
			}

			// Sanity-check the schema actually landed.
			db, err := sql.Open("postgres", freshURI)
			if err != nil {
				t.Fatalf("failed opening migrated database: %v", err)
			}
			defer db.Close()
			var count int
			if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'private'").Scan(&count); err != nil {
				t.Fatalf("failed inspecting migrated schema: %v", err)
			}
			if count == 0 {
				t.Fatal("expected migrated tables in the private schema, found none")
			}
		})
	}
}

// stageEmbeddedMigrations copies the migration files matching include from
// the embedded filesystem into a temp dir, mirroring how release artifacts
// place them on disk.
func stageEmbeddedMigrations(t *testing.T, include func(name string) bool) string {
	t.Helper()
	migrationDir := t.TempDir()
	entries, err := migrationfiles.FS.ReadDir(".")
	if err != nil {
		t.Fatalf("failed reading embedded migrations: %v", err)
	}
	var staged int
	for _, entry := range entries {
		if !include(entry.Name()) {
			continue
		}
		data, err := migrationfiles.FS.ReadFile(entry.Name())
		if err != nil {
			t.Fatalf("failed reading embedded migration %s: %v", entry.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(migrationDir, entry.Name()), data, 0o600); err != nil {
			t.Fatalf("failed staging migration %s: %v", entry.Name(), err)
		}
		staged++
	}
	if staged == 0 {
		t.Fatal("no migration files matched in the embedded filesystem")
	}
	return migrationDir
}

// createFreshDatabase creates a uniquely named database on the suite's
// PostgreSQL container (the suite database is already migrated by TestMain)
// and drops it on cleanup.
func createFreshDatabase(t *testing.T) string {
	t.Helper()
	pg := testGateway.Postgres
	dbName := fmt.Sprintf("migration_pathfiles_%08x", rand.Uint32())

	adminConn, err := sql.Open("postgres", pg.URI())
	if err != nil {
		t.Fatalf("failed opening admin connection: %v", err)
	}
	if _, err := adminConn.Exec("CREATE DATABASE " + dbName); err != nil {
		adminConn.Close()
		t.Fatalf("failed creating database %s: %v", dbName, err)
	}
	t.Cleanup(func() {
		defer adminConn.Close()
		if _, err := adminConn.Exec("DROP DATABASE " + dbName + " WITH (FORCE)"); err != nil {
			t.Logf("failed dropping database %s: %v", dbName, err)
		}
	})

	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		pg.User, pg.Password, pg.Host, pg.Port, dbName)
}
