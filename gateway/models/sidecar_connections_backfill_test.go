package models_test

import (
	"context"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
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
	const flagOffOrgID = "00000000-0000-0000-0000-0000000000f0"
	withDB(t, inst, func() {
		execSQL(t, `INSERT INTO private.orgs (id, name) VALUES (?, 'backfill-test')`, testOrgID)
		// The backfill mirrors only the orgs that opted in, as the write path.
		execSQL(t, `INSERT INTO private.org_feature_flags (org_id, name, enabled) VALUES (?, 'beta.sidecar_listeners', true)`, testOrgID)
		execSQL(t, `INSERT INTO private.orgs (id, name) VALUES (?, 'backfill-off')`, flagOffOrgID)
		execSQL(t, `INSERT INTO private.sidecars (org_id, name, key_hash, created_by, configuration)
			VALUES (?, 'pay', 'h-off-pay', 'tests@hoop.dev', '{"listeners":[{"name":"appdb","protocol":"postgres"}]}'::jsonb)`, flagOffOrgID)
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
		// "long-" plus 124 is 129, one over resources.name.
		sidecar("long", `{"listeners":[{"name":"`+strings.Repeat("a", 124)+`","protocol":"postgres"},{"name":"fits","protocol":"postgres"}]}`)
		// The admin's own connection has the name conf's listener composes.
		execSQL(t, `INSERT INTO private.resources (org_id, name, type, subtype) VALUES (?, 'conf-appdb', 'database', 'postgres')`, testOrgID)
		execSQL(t, `INSERT INTO private.connections (org_id, name, type, subtype, resource_name) VALUES (?, 'conf-appdb', 'database', 'postgres', 'conf-appdb')`, testOrgID)
		// A resource a deleted connection left behind, with a mirror's name.
		execSQL(t, `INSERT INTO private.resources (org_id, name, type, subtype) VALUES (?, 'pay-appdb', 'custom', 'stale')`, testOrgID)
		// A resource with a mirror's name that an admin connection still uses.
		execSQL(t, `INSERT INTO private.resources (org_id, name, type, subtype) VALUES (?, 'pay-api', 'custom', 'loki')`, testOrgID)
		execSQL(t, `INSERT INTO private.connections (org_id, name, type, subtype, resource_name) VALUES (?, 'logs', 'custom', 'loki', 'pay-api')`, testOrgID)
		// A resource with a mirror's name that names an agent and no connection.
		agentID := uuid.NewString()
		execSQL(t, `INSERT INTO private.agents (id, org_id, name, mode, key_hash, status) VALUES (?, ?, 'q-agent', 'standard', 'x', 'DISCONNECTED')`, agentID, testOrgID)
		sidecar("agentres", `{"listeners":[{"name":"q","protocol":"postgres"}]}`)
		execSQL(t, `INSERT INTO private.resources (org_id, name, type, subtype, agent_id) VALUES (?, 'agentres-q', 'custom', 'redis', ?)`, testOrgID, agentID)
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
			"pay-x-y":   "pay/x-y database/mysql res=database/mysql",
			"weird-ok":  "weird/ok application/ssh res=application/ssh",
			"file-g":    "file/g custom/grpc res=custom/grpc",
			"long-fits": "long/fits database/postgres res=database/postgres",
		}
		if len(got) != len(want) {
			t.Errorf("want %d mirrors, got %d: %v", len(want), len(got), got)
		}
		for name, desc := range want {
			if got[name] != desc {
				t.Errorf("%s: want %q, got %q", name, desc, got[name])
			}
		}
		// The gateway has no route to a sidecar: no access mode is offered.
		if n := queryString(t, `SELECT count(*)::text FROM private.connections WHERE org_id = ? AND managed_by = 'sidecar'
			AND 'disabled' = ALL (ARRAY[access_mode_connect, access_mode_exec, access_mode_runbooks, access_schema])`, testOrgID); n != "5" {
			t.Errorf("want every access mode of the 5 mirrors disabled, got %s mirrors so", n)
		}
		if s := queryString(t, `SELECT COALESCE(managed_by, '') || '|' || COALESCE(sidecar_id::text, '') FROM private.connections WHERE org_id = ? AND name = 'conf-appdb'`, testOrgID); s != "|" {
			t.Errorf("the admin's connection was taken over: %q", s)
		}
		if kind := queryString(t, `SELECT type || '/' || subtype FROM private.resources WHERE org_id = ? AND name = 'pay-api'`, testOrgID); kind != "custom/loki" {
			t.Errorf("the resource the admin's connection uses was rewritten: %s", kind)
		}
		if kind := queryString(t, `SELECT type || '/' || subtype FROM private.resources WHERE org_id = ? AND name = 'agentres-q'`, testOrgID); kind != "custom/redis" {
			t.Errorf("the agent's resource was rewritten: %s", kind)
		}
		// Its own query text: the embedded backend keeps one server session,
		// so a statement the driver prepared under the same text later in
		// this test would collide with it.
		if n := queryString(t, `SELECT count(*)::text FROM private.connections c WHERE c.org_id = ?`, flagOffOrgID); n != "0" {
			t.Errorf("an org with the flag off got %s connections", n)
		}
		// What the down must clear before it can delete the mirror.
		execSQL(t, `INSERT INTO private.event_subscriptions
			(org_id, name, event_types, runbook_repository, runbook_file, connection_name, created_by_user_id, created_by_email)
			VALUES (?, 'on-deny', '{guardrail.denied}', 'repo', 'notify.runbook.sh', 'pay-appdb', 'u1', 'admin@hoop.dev')`, testOrgID)
	})

	// Down removes every mirror, the subscriptions on them and the resources
	// they alone used; the admin's connections stay.
	migrateTo(t, inst, func(m *migrate.Migrate) error { return m.Steps(-1) })
	withDB(t, inst, func() {
		if n := queryString(t, `SELECT count(*)::text FROM private.connections WHERE org_id = ?`, testOrgID); n != "2" {
			t.Errorf("want only the admin's connections after down, got %s", n)
		}
		if n := queryString(t, `SELECT count(*)::text FROM private.resources WHERE org_id = ?`, testOrgID); n != "3" {
			t.Errorf("want only the admin's and the agent's resources after down, got %s", n)
		}
		if n := queryString(t, `SELECT count(*)::text FROM private.event_subscriptions WHERE org_id = ?`, testOrgID); n != "0" {
			t.Errorf("the subscription on a mirror outlived it")
		}
	})
}
