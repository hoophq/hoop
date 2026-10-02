package models_test

import (
	"context"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	migrationfiles "github.com/hoophq/hoop/gateway/migrations"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite"
)

// The migration that adds the binding columns, after which the backfill runs.
const bindingColumnsVersion = 128

// migrateTo steps the embedded migrations and releases the connection: the
// embedded backend serves one session at a time.
func migrateTo(t *testing.T, inst *pglite.Instance, step func(m *migrate.Migrate) error) {
	t.Helper()
	src, err := iofs.New(migrationfiles.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, inst.MigrateDSN())
	if err != nil {
		t.Fatal(err)
	}
	if err := step(m); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := m.Close(); err != nil {
		t.Fatalf("close migrate: %v", err)
	}
}

// withDB points models.DB at the instance for the duration of fn, then
// releases the pool so a migration can take the session.
func withDB(t *testing.T, inst *pglite.Instance, fn func()) {
	t.Helper()
	if err := models.InitDatabaseConnection(inst.DSN(), 1); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := models.DB.DB()
	if err != nil {
		t.Fatal(err)
	}
	fn()
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestBackfillMirrorsExistingListeners(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	ctx := context.Background()
	inst, err := pglite.Start(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("start embedded database: %v", err)
	}
	t.Cleanup(func() { inst.Close(ctx) })

	migrateTo(t, inst, func(m *migrate.Migrate) error { return m.Migrate(bindingColumnsVersion) })

	// Rows as a gateway before the projection left them.
	withDB(t, inst, func() {
		execSQL(t, `INSERT INTO private.orgs (id, name) VALUES (?, 'backfill-test')`, testOrgID)
		sidecar := func(name, cfg string) {
			execSQL(t, `INSERT INTO private.sidecars (org_id, name, key_hash, created_by, configuration, created_at)
				VALUES (?, ?, ?, 'tests@hoop.dev', ?::jsonb, clock_timestamp())`, testOrgID, name, "h-"+name, cfg)
		}
		sidecar("pay", `{"listeners":[
			{"name":"appdb","protocol":"postgres"},{"name":"api","protocol":"http"},{"name":"x-y","protocol":"mysql"}]}`)
		sidecar("conf", `{"listeners":[{"name":"appdb","protocol":"postgres"}]}`)
		sidecar("weird", `{"listeners":[{"name":"app db","protocol":"postgres"},{"name":"ok","protocol":"ssh"},{"protocol":"mysql"}]}`)
		sidecar("ora", `{"listeners":[{"name":"x","protocol":"oracle"}]}`)
		sidecar("pay-x", `{"listeners":[{"name":"y","protocol":"mssql"}]}`)
		sidecar("file", `{"load_from_disk":true,"listeners":[{"name":"g","protocol":"grpc"}]}`)
		// The admin's own connection has the name conf's listener composes.
		execSQL(t, `INSERT INTO private.resources (org_id, name, type, subtype) VALUES (?, 'conf-appdb', 'database', 'postgres')`, testOrgID)
		execSQL(t, `INSERT INTO private.connections (org_id, name, type, subtype, resource_name) VALUES (?, 'conf-appdb', 'database', 'postgres', 'conf-appdb')`, testOrgID)
		// A resource a deleted connection left behind, with a mirror's name.
		execSQL(t, `INSERT INTO private.resources (org_id, name, type, subtype) VALUES (?, 'pay-appdb', 'custom', 'stale')`, testOrgID)
	})

	migrateTo(t, inst, func(m *migrate.Migrate) error { return m.Up() })

	withDB(t, inst, func() {
		var rows []struct {
			Name     string `gorm:"column:name"`
			Sidecar  string `gorm:"column:sidecar"`
			Listener string `gorm:"column:sidecar_listener"`
			Kind     string `gorm:"column:kind"`
			ResKind  string `gorm:"column:res_kind"`
		}
		err := models.DB.Raw(`
		SELECT c.name, s.name AS sidecar, c.sidecar_listener, c.type || '/' || c.subtype AS kind, r.type || '/' || r.subtype AS res_kind
		FROM private.connections c
		JOIN private.sidecars s ON s.id = c.sidecar_id
		JOIN private.resources r ON r.org_id = c.org_id AND r.name = c.resource_name
		WHERE c.org_id = ? AND c.managed_by = 'sidecar' ORDER BY c.name`, testOrgID).Scan(&rows).Error
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for _, r := range rows {
			got[r.Name] = r.Sidecar + "/" + r.Listener + " " + r.Kind + " res=" + r.ResKind
		}
		want := map[string]string{
			"pay-appdb": "pay/appdb database/postgres res=database/postgres",
			"pay-api":   "pay/api httpproxy/httpproxy res=httpproxy/httpproxy",
			"pay-x-y":   "pay/x-y database/mysql res=database/mysql",
			"weird-ok":  "weird/ok application/ssh res=application/ssh",
			"file-g":    "file/g custom/grpc res=custom/grpc",
		}
		if len(got) != len(want) {
			t.Errorf("want %d mirrors, got %d: %v", len(want), len(got), got)
		}
		for name, desc := range want {
			if got[name] != desc {
				t.Errorf("%s: want %q, got %q", name, desc, got[name])
			}
		}
		if s := queryString(t, `SELECT COALESCE(managed_by, '') || '|' || COALESCE(sidecar_id::text, '') FROM private.connections WHERE org_id = ? AND name = 'conf-appdb'`, testOrgID); s != "|" {
			t.Errorf("the admin's connection was taken over: %q", s)
		}
	})

	// Down removes every mirror and the resources they alone used; the
	// admin's connection stays.
	migrateTo(t, inst, func(m *migrate.Migrate) error { return m.Steps(-1) })
	withDB(t, inst, func() {
		if n := queryString(t, `SELECT count(*)::text FROM private.connections WHERE org_id = ?`, testOrgID); n != "1" {
			t.Errorf("want only the admin's connection after down, got %s", n)
		}
		if n := queryString(t, `SELECT count(*)::text FROM private.resources WHERE org_id = ?`, testOrgID); n != "1" {
			t.Errorf("want only the admin's resource after down, got %s", n)
		}
	})
}
