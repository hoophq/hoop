package pglite

import (
	"context"
	"database/sql"
	"testing"

	modelsbootstrap "github.com/hoophq/hoop/gateway/models/bootstrap"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestStartQueryAndResume boots the embedded database, exercises the PG
// features the gateway data layer relies on through the same pgx driver
// production uses, then restarts the instance on the same data directory to
// validate clean shutdown, cluster resume and data persistence.
func TestStartQueryAndResume(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping embedded pglite test in -short mode")
	}
	ctx := context.Background()
	dataDir := t.TempDir()

	inst, err := Start(ctx, dataDir)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	db := openDB(t, inst.DSN())
	var version string
	if err := db.QueryRow(`SELECT version()`).Scan(&version); err != nil {
		t.Fatalf("SELECT version(): %v", err)
	}
	t.Logf("connected: %s", version)

	// Schema-qualified DDL, mirroring how hoop migrations and models
	// reference objects (see DSN docs about the backend's search_path).
	if _, err := db.Exec(`
		CREATE TABLE public.pglite_smoke (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			labels JSONB NOT NULL DEFAULT '{}'::jsonb,
			command TEXT[]
		)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	var id string
	if err := db.QueryRow(`
		INSERT INTO public.pglite_smoke (labels, command)
		VALUES ('{"env": "test"}'::jsonb, ARRAY['echo', 'hi'])
		RETURNING id::text`).Scan(&id); err != nil {
		t.Fatalf("insert returning: %v", err)
	}

	// Sequential UUID generation must yield distinct values: the guest
	// reads entropy from the virtual /dev/urandom, and a static seed file
	// would replay identical bytes (regression: duplicate primary keys on
	// multi-row inserts with uuid defaults).
	seen := map[string]bool{}
	for n := 0; n < 5; n++ {
		var u string
		if err := db.QueryRow(`SELECT gen_random_uuid()::text`).Scan(&u); err != nil {
			t.Fatalf("gen_random_uuid call %d: %v", n, err)
		}
		if seen[u] {
			t.Fatalf("gen_random_uuid returned a duplicate value %q on call %d", u, n)
		}
		seen[u] = true
	}
	if _, err := db.Exec(`
		INSERT INTO public.pglite_smoke (labels) VALUES ('{}'), ('{}'), ('{}')`); err != nil {
		t.Fatalf("multi-row insert with uuid default: %v", err)
	}

	// SQL errors must surface as SQLSTATE errors without killing the
	// backend (GORM error translation depends on this).
	if _, err := db.Exec(`SELECT * FROM public.does_not_exist`); err == nil {
		t.Fatal("expected error from missing table, got none")
	}
	var one int
	if err := db.QueryRow(`SELECT 1`).Scan(&one); err != nil || one != 1 {
		t.Fatalf("backend did not survive SQL error: %v", err)
	}

	// Errors inside explicit transactions must not brick the backend: this
	// build cannot always roll back a failed transaction (active portal),
	// so the bridge reboots the backend, which is semantically the
	// rollback. The failed transaction's writes must be discarded and
	// subsequent sessions must work.
	if _, err := db.Exec(`CREATE TABLE public.tx_probe (v INT PRIMARY KEY)`); err != nil {
		t.Fatalf("create tx_probe: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO public.tx_probe (v) VALUES (1)`); err != nil {
		t.Fatalf("seed tx_probe: %v", err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(`INSERT INTO public.tx_probe (v) VALUES (2)`); err != nil {
		t.Fatalf("insert in tx: %v", err)
	}
	if _, err := tx.Exec(`INSERT INTO public.tx_probe (v) VALUES (1)`); err == nil {
		t.Fatal("expected unique violation inside transaction, got none")
	}
	_ = tx.Rollback() // may fail with the session reset; either way the tx must be gone
	var probeCount int
	if err := db.QueryRow(`SELECT count(*) FROM public.tx_probe`).Scan(&probeCount); err != nil {
		t.Fatalf("query tx_probe after failed transaction: %v", err)
	}
	if probeCount != 1 {
		t.Fatalf("failed transaction leaked writes: expected 1 row, got %d", probeCount)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	if err := inst.Close(ctx); err != nil {
		t.Fatalf("close instance: %v", err)
	}

	// Resume on the same data directory: cluster must come back with data.
	inst2, err := Start(ctx, dataDir)
	if err != nil {
		t.Fatalf("resume start: %v", err)
	}
	defer inst2.Close(ctx)

	db2 := openDB(t, inst2.DSN())
	defer db2.Close()
	var env string
	if err := db2.QueryRow(`SELECT labels->>'env' FROM public.pglite_smoke WHERE id = $1`, id).Scan(&env); err != nil {
		t.Fatalf("select after resume: %v", err)
	}
	if env != "test" {
		t.Fatalf("unexpected value after resume: %q", env)
	}
}

// TestHoopMigrations applies the entire embedded hoop migration set against
// the embedded database, then restarts the instance and applies them again —
// the upgrade path every release exercises: an existing cluster must
// recognize the already-applied version from the pinned migrations table.
func TestHoopMigrations(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping embedded pglite test in -short mode")
	}
	ctx := context.Background()
	dataDir := t.TempDir()
	inst, err := Start(ctx, dataDir)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	if err := modelsbootstrap.MigrateDB(inst.MigrateDSN(), ""); err != nil {
		t.Fatalf("migrations failed: %v", err)
	}

	// Sessions are serialized by the bridge: a fresh connection after the
	// migration session must work, and core objects must exist.
	db := openDB(t, inst.DSN())
	var orgCount int
	if err := db.QueryRow(`SELECT count(*) FROM private.orgs`).Scan(&orgCount); err != nil {
		t.Fatalf("query private.orgs after migrations: %v", err)
	}
	var enumCount int
	if err := db.QueryRow(`SELECT count(*) FROM pg_type WHERE typtype = 'e'`).Scan(&enumCount); err != nil {
		t.Fatalf("query enum types: %v", err)
	}
	if enumCount == 0 {
		t.Fatal("expected migration-created enum types, found none")
	}
	var versionTableSchema string
	if err := db.QueryRow(`
		SELECT relnamespace::regnamespace::text FROM pg_class WHERE relname = 'schema_migrations'`).
		Scan(&versionTableSchema); err != nil {
		t.Fatalf("locate schema_migrations: %v", err)
	}
	if versionTableSchema != "public" {
		t.Fatalf("schema_migrations must be pinned to public, found %q", versionTableSchema)
	}
	db.Close()
	if err := inst.Close(ctx); err != nil {
		t.Fatalf("close instance: %v", err)
	}
	t.Logf("migrations applied on first boot: %d enum types, version table in %s", enumCount, versionTableSchema)

	// Second boot: re-running migrations must be a no-op, not a re-apply.
	inst2, err := Start(ctx, dataDir)
	if err != nil {
		t.Fatalf("resume start: %v", err)
	}
	defer inst2.Close(ctx)
	if err := modelsbootstrap.MigrateDB(inst2.MigrateDSN(), ""); err != nil {
		t.Fatalf("migrations on resumed cluster failed: %v", err)
	}

	db2 := openDB(t, inst2.DSN())
	defer db2.Close()
	if err := db2.QueryRow(`SELECT count(*) FROM private.orgs`).Scan(&orgCount); err != nil {
		t.Fatalf("query private.orgs after resumed migration run: %v", err)
	}
	t.Log("upgrade path ok: migrations no-op on resumed cluster")
}

func openDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open %s: %v", dsn, err)
	}
	// The embedded backend serves one session at a time.
	db.SetMaxOpenConns(1)
	return db
}
