package modelsbootstrap_test

import (
	"database/sql"
	"os"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	migrationfiles "github.com/hoophq/hoop/gateway/migrations"
	"github.com/hoophq/hoop/gateway/pglite"
	"github.com/hoophq/hoop/gateway/pglite/pglitetest"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) { os.Exit(pglitetest.Main(m)) }

// migrateTo moves the schema to version and closes the migrate instance, so
// the embedded backend, which serves one session at a time, is free again.
func migrateTo(t *testing.T, inst *pglite.Instance, version uint) {
	t.Helper()
	src, err := iofs.New(migrationfiles.FS, ".")
	require.NoError(t, err)
	m, err := migrate.NewWithSourceInstance("iofs", src, inst.MigrateDSN())
	require.NoError(t, err)
	require.NoError(t, m.Migrate(version))
	srcErr, dbErr := m.Close()
	require.NoError(t, srcErr)
	require.NoError(t, dbErr)
}

func hasLastLoginColumn(t *testing.T, inst *pglite.Instance) bool {
	t.Helper()
	// lib/pq, not pgx: pgx caches named prepared statements, and the embedded
	// backend keeps them across connections, so a second pgx pool collides.
	db, err := sql.Open("postgres", inst.DSN())
	require.NoError(t, err)
	defer db.Close()
	var found bool
	require.NoError(t, db.QueryRow(`
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = 'private' AND table_name = 'users' AND column_name = 'last_login_at')`).Scan(&found))
	return found
}

// Migration 000140 rolls back: down drops users.last_login_at, up restores it.
func TestUsersLastLoginMigrationRollsBack(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	inst := pglitetest.StartMigrated(t)
	require.True(t, hasLastLoginColumn(t, inst))

	migrateTo(t, inst, 139)
	assert.False(t, hasLastLoginColumn(t, inst), "down must drop users.last_login_at")

	migrateTo(t, inst, 140)
	assert.True(t, hasLastLoginColumn(t, inst), "up must add users.last_login_at again")
}
