package models_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite"
)

// listenerNamesTextVersion makes every listener name column TEXT.
const listenerNamesTextVersion = 138

// assertListenerColumns checks that every column storing a listener name has
// type typ.
func assertListenerColumns(t *testing.T, typ string) {
	t.Helper()
	var rows []struct {
		Column string `gorm:"column:col"`
		Type   string `gorm:"column:typ"`
	}
	err := models.DB.Raw(`SELECT table_name || '.' || column_name AS col,
		data_type || COALESCE('(' || character_maximum_length || ')', '') AS typ
		FROM information_schema.columns
		WHERE table_schema = 'private' AND column_name IN ('listener_name', 'sidecar_listener')`).Scan(&rows).Error
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range rows {
		got[r.Column] = r.Type
	}
	want := map[string]string{}
	for _, c := range []string{"connections.sidecar_listener", "reviews.listener_name",
		"guardrail_rules_listeners.listener_name", "datamasking_rules_listeners.listener_name",
		"ai_session_analyzer_rules_listeners.listener_name", "sidecar_slack_channels.listener_name"} {
		want[c] = typ
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("listener name columns = %v, want all %s", got, typ)
	}
}

// The up takes a name of any length. The down refuses a name it would cut,
// and runs once no such name is stored.
func TestListenerNameColumnsTakeAnyLength(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	ctx := context.Background()
	inst, err := pglite.Start(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("start embedded database: %v", err)
	}
	t.Cleanup(func() { inst.Close(ctx) })

	migrateTo(t, inst, func(m *migrate.Migrate) error { return m.Migrate(listenerNamesTextVersion - 1) })
	withDB(t, inst, func() {
		assertListenerColumns(t, "character varying(255)")
	})

	migrateTo(t, inst, func(m *migrate.Migrate) error { return m.Migrate(listenerNamesTextVersion) })
	long := strings.Repeat("l", 300)
	withDB(t, inst, func() {
		assertListenerColumns(t, "text")
		execSQL(t, `INSERT INTO private.orgs (id, name) VALUES (?, 'listener-names')`, testOrgID)
		sc := &models.Sidecar{OrgID: testOrgID, Name: "edge3", KeyHash: models.HashAPIKey("hsc_edge3"), CreatedBy: "tests@hoop.dev"}
		if err := models.CreateSidecar(models.DB, sc); err != nil {
			t.Fatal(err)
		}
		execSQL(t, `INSERT INTO private.sidecar_slack_channels (org_id, sidecar_id, listener_name, channels)
			VALUES (?, ?, ?, '{C0123}')`, testOrgID, sc.ID, long)
	})

	migrateTo(t, inst, func(m *migrate.Migrate) error {
		err := m.Migrate(listenerNamesTextVersion - 1)
		if err == nil || !strings.Contains(err.Error(), "value too long") {
			t.Errorf("the down must refuse a 300-character name, got %v", err)
		}
		return nil
	})
	// A migrate CLI exits here and Postgres ends its session. The embedded
	// backend keeps that one session: end its failed transaction and lock.
	withDB(t, inst, func() {
		execSQL(t, `ROLLBACK`)
		execSQL(t, `SELECT pg_advisory_unlock_all()`)
	})
	// The down ran in one transaction, so the schema is still at the up.
	migrateTo(t, inst, func(m *migrate.Migrate) error { return m.Force(listenerNamesTextVersion) })
	withDB(t, inst, func() {
		assertListenerColumns(t, "text")
		var stored string
		if err := models.DB.Raw(`SELECT listener_name FROM private.sidecar_slack_channels`).Scan(&stored).Error; err != nil {
			t.Fatal(err)
		}
		if stored != long {
			t.Errorf("the refused down changed the name to %d characters", len(stored))
		}
		execSQL(t, `DELETE FROM private.sidecar_slack_channels`)
	})

	migrateTo(t, inst, func(m *migrate.Migrate) error { return m.Migrate(listenerNamesTextVersion - 1) })
	withDB(t, inst, func() {
		assertListenerColumns(t, "character varying(255)")
	})
}
