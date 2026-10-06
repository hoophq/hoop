package services

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
	migrationfiles "github.com/hoophq/hoop/gateway/migrations"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

// The schema before and after migration 000133, which copies sidecar rule
// bindings onto mirror connections.
const (
	crossVersionBefore = 132
	crossVersionAfter  = 133
	crossVersionOrgID  = "00000000-0000-0000-0000-0000000e0525"
)

// legacyRulesForSidecar is how a gateway before 000133 reads the rules of one
// sidecar: the queries of origin/main at f985e0723, kept here verbatim. During
// a rolling deploy or after an image rollback, that gateway serves sidecars
// from these rows alone. Do not change them when the current readers change:
// they stand for a released binary.
func legacyRulesForSidecar(t *testing.T, db *gorm.DB, sidecarID string) (g, m, a []models.BoundRule) {
	t.Helper()
	org := uuid.MustParse(crossVersionOrgID)
	for _, q := range []struct {
		out                       *[]models.BoundRule
		listeners, rules, ruleCol string
	}{
		{&g, "guardrail_rules_listeners", "guardrail_rules", "guardrail_rule_name"},
		{&m, "datamasking_rules_listeners", "datamasking_rules", "datamasking_rule_name"},
		{&a, "ai_session_analyzer_rules_listeners", "ai_session_analyzer_rules", "analyzer_rule_name"},
	} {
		err := db.Raw(`
	SELECT b.listener_name, r.name AS rule_name, r.sidecar_spec
	FROM private.`+q.listeners+` b
	JOIN private.`+q.rules+` r ON r.org_id = b.org_id AND r.name = b.`+q.ruleCol+`
	WHERE b.org_id = ? AND b.sidecar_id = ?
	ORDER BY b.listener_name, b.position, r.name`, org, sidecarID).Scan(q.out).Error
		if err != nil {
			t.Fatalf("legacy read of %s: %v", q.listeners, err)
		}
	}
	return g, m, a
}

// served is the document each sidecar receives: from this gateway, and from a
// gateway before 000133 reading the same database.
func served(t *testing.T, sidecars ...*models.Sidecar) (current, legacy map[string]string) {
	t.Helper()
	current, legacy = map[string]string{}, map[string]string{}
	for _, ref := range sidecars {
		sc, err := models.GetSidecarByNameOrID(models.DB, crossVersionOrgID, ref.ID)
		if err != nil {
			t.Fatalf("read sidecar %s: %v", ref.Name, err)
		}
		composed, err := ComposeSidecarConfiguration(models.DB, sc)
		if err != nil {
			t.Fatalf("compose %s: %v", sc.Name, err)
		}
		g, m, a := legacyRulesForSidecar(t, models.DB, sc.ID)
		old, err := foldSidecarRules(daemon.Config(sc.Configuration), g, m, a)
		if err != nil {
			t.Fatalf("legacy compose %s: %v", sc.Name, err)
		}
		current[sc.Name], legacy[sc.Name] = mustJSON(t, composed), mustJSON(t, old)
	}
	return current, legacy
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// dbState is every row of every table in the private and public schemas, as
// JSON, per table.
func dbState(t *testing.T) map[string][]string {
	t.Helper()
	var tables []string
	err := models.DB.Raw(`SELECT table_schema || '.' || table_name FROM information_schema.tables
	WHERE table_schema IN ('private', 'public') AND table_type = 'BASE TABLE'`).Scan(&tables).Error
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	out := map[string][]string{}
	for _, tbl := range tables {
		var rows []string
		if err := models.DB.Raw(`SELECT row_to_json(t)::text FROM ` + tbl + ` t`).Scan(&rows).Error; err != nil {
			t.Fatalf("dump %s: %v", tbl, err)
		}
		slices.Sort(rows)
		out[tbl] = rows
	}
	return out
}

// changedTables names every table whose rows differ between two states.
func changedTables(a, b map[string][]string) []string {
	var out []string
	for tbl := range a {
		if !slices.Equal(a[tbl], b[tbl]) {
			out = append(out, tbl)
		}
	}
	for tbl := range b {
		if _, ok := a[tbl]; !ok {
			out = append(out, tbl)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func crossVersionMigrate(t *testing.T, inst *pglite.Instance, version uint) {
	t.Helper()
	src, err := iofs.New(migrationfiles.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, inst.MigrateDSN())
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Migrate(version); err != nil {
		t.Fatalf("migrate to %d: %v", version, err)
	}
	if _, err := m.Close(); err != nil {
		t.Fatal(err)
	}
}

// crossVersionSession opens the pool for fn and closes it after. The embedded
// backend keeps one session, and with it the statements the last pool
// prepared, so each session starts by dropping them.
func crossVersionSession(t *testing.T, inst *pglite.Instance, fn func()) {
	t.Helper()
	if err := models.InitDatabaseConnection(inst.DSN(), 1); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := models.DB.DB()
	if err != nil {
		t.Fatal(err)
	}
	crossVersionExec(t, `DEALLOCATE ALL`)
	fn()
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
}

func crossVersionExec(t *testing.T, q string, args ...any) {
	t.Helper()
	if err := models.DB.Exec(q, args...).Error; err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

// TestSidecarRulesAcrossGatewayVersions walks migration 000133 the way a
// deploy meets it, and checks two things at every step:
//
//   - what this gateway serves each sidecar equals what a gateway before
//     000133 serves from the same database (legacyRulesForSidecar), and equals
//     the document before the migration;
//   - the whole database changes only where that step should change it.
//
// Steps: rows as origin/main stores them; migration up; an older gateway
// unbinds two rules; this gateway's mirror writer syncs; migration down.
func TestSidecarRulesAcrossGatewayVersions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	ctx := context.Background()
	inst, err := pglite.Start(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("start embedded database: %v", err)
	}
	t.Cleanup(func() { inst.Close(ctx) })
	crossVersionMigrate(t, inst, crossVersionBefore)

	org := uuid.MustParse(crossVersionOrgID)
	var pay, legacySc *models.Sidecar
	var baseline map[string]string
	var s1 map[string][]string

	// 1. Rows as origin/main stores them: bindings by listener name only.
	// pay has mirrors (flag on when it was written); legacy has none.
	crossVersionSession(t, inst, func() {
		crossVersionExec(t, `INSERT INTO private.orgs (id, name) VALUES (?, 'eng-525')`, crossVersionOrgID)
		newSidecar := func(name string, listeners ...string) *models.Sidecar {
			sc := &models.Sidecar{OrgID: crossVersionOrgID, Name: name, KeyHash: models.HashAPIKey("hsc_" + name),
				CreatedBy: "tests@hoop.dev"}
			for i, l := range listeners {
				sc.Configuration.Listeners = append(sc.Configuration.Listeners, daemon.ListenerConfig{
					Name: l, Protocol: "postgres", Listen: fmt.Sprintf(":%d", 5432+i), Upstream: "db:5432"})
			}
			if err := models.CreateSidecar(models.DB, sc); err != nil {
				t.Fatalf("seed sidecar %s: %v", name, err)
			}
			return sc
		}
		pay, legacySc = newSidecar("pay", "appdb", "reports"), newSidecar("legacy", "orders")
		mirrors, err := ProjectListeners(crossVersionOrgID, pay)
		if err != nil {
			t.Fatal(err)
		}
		if err := models.DB.Transaction(func(tx *gorm.DB) error {
			return models.SyncSidecarConnectionsTx(tx, crossVersionOrgID, pay.ID, mirrors)
		}); err != nil {
			t.Fatalf("seed pay mirrors: %v", err)
		}
		for _, r := range []struct{ name, op string }{{"g-a", "truncate"}, {"g-b", "drop"}, {"g-c", "delete"}} {
			rule := &models.GuardRailRules{OrgID: crossVersionOrgID, ID: uuid.NewString(), Name: r.name,
				Input: map[string]any{}, Output: map[string]any{},
				SidecarSpec: json.RawMessage(`{"rules":[{"name":"` + r.name + `","type":"operation","operations":["` + r.op + `"]}]}`)}
			if err := models.UpsertGuardRailRuleWithConnections(rule, nil, true); err != nil {
				t.Fatal(err)
			}
		}
		// A gateway rule bound to a mirror through connection_ids: it is the
		// rule's connection list, and no step may touch it.
		gw := &models.GuardRailRules{OrgID: crossVersionOrgID, ID: uuid.NewString(), Name: "gw-on-mirror",
			Input: map[string]any{}, Output: map[string]any{}}
		var appdbMirror string
		if err := models.DB.Raw(`SELECT id FROM private.connections WHERE sidecar_id = ? AND sidecar_listener = 'appdb'`,
			pay.ID).Scan(&appdbMirror).Error; err != nil || appdbMirror == "" {
			t.Fatalf("find the appdb mirror: %v", err)
		}
		if err := models.UpsertGuardRailRuleWithConnections(gw, []string{appdbMirror}, true); err != nil {
			t.Fatal(err)
		}
		mask := &models.DataMaskingRule{ID: uuid.NewString(), OrgID: crossVersionOrgID, Name: "m-email",
			SupportedEntityTypes: models.SupportedEntityTypesList{}, CustomEntityTypes: models.CustomEntityTypesList{},
			SidecarSpec: json.RawMessage(`{"rules":[{"entities":["EMAIL_ADDRESS"],"strategy":"mask"}]}`)}
		if _, err := models.CreateDataMaskingRule(mask); err != nil {
			t.Fatal(err)
		}
		an := &models.AISessionAnalyzerRules{OrgID: org, Name: "an-deletes", ConnectionNames: pq.StringArray{},
			SidecarSpec: json.RawMessage(`{"trigger":{"operations":["delete"]},"high":"block"}`)}
		if err := models.CreateAISessionAnalyzerRule(an); err != nil {
			t.Fatal(err)
		}
		// Positions run against name order, so a step that drops them
		// reorders the lane.
		for _, b := range []struct {
			table, col, rule string
			sc               *models.Sidecar
			listener         string
			pos              int
		}{
			{"guardrail_rules_listeners", "guardrail_rule_name", "g-b", pay, "appdb", 0},
			{"guardrail_rules_listeners", "guardrail_rule_name", "g-a", pay, "appdb", 1},
			{"guardrail_rules_listeners", "guardrail_rule_name", "g-c", pay, "reports", 0},
			{"guardrail_rules_listeners", "guardrail_rule_name", "g-a", legacySc, "orders", 0},
			{"datamasking_rules_listeners", "datamasking_rule_name", "m-email", pay, "appdb", 0},
			{"datamasking_rules_listeners", "datamasking_rule_name", "m-email", legacySc, "orders", 0},
			{"ai_session_analyzer_rules_listeners", "analyzer_rule_name", "an-deletes", pay, "appdb", 0},
		} {
			crossVersionExec(t, `INSERT INTO private.`+b.table+` (org_id, `+b.col+`, sidecar_id, listener_name, position)
				VALUES (?, ?, ?, ?, ?)`, org, b.rule, b.sc.ID, b.listener, b.pos)
		}
		var legacy map[string]string
		baseline, legacy = served(t, pay, legacySc)
		if !strings.Contains(baseline["pay"], `"g-b"`) || strings.Index(baseline["pay"], `"g-b"`) > strings.Index(baseline["pay"], `"g-a"`) ||
			!strings.Contains(baseline["pay"], "EMAIL_ADDRESS") || !strings.Contains(baseline["legacy"], `"g-a"`) {
			t.Fatalf("the baseline must carry every seeded rule, g-b before g-a; got %v", baseline)
		}
		for name := range baseline {
			if legacy[name] != baseline[name] {
				t.Fatalf("%s: the legacy reader and this gateway disagree before the migration\nlegacy:  %s\ncurrent: %s",
					name, legacy[name], baseline[name])
			}
		}
		s1 = dbState(t)
	})

	sameServed := func(step string) {
		t.Helper()
		current, legacy := served(t, pay, legacySc)
		for name, want := range baseline {
			if current[name] != want {
				t.Errorf("%s: this gateway serves %s a different document\nwant: %s\ngot:  %s", step, name, want, current[name])
			}
			if legacy[name] != want {
				t.Errorf("%s: a gateway before 000133 serves %s a different document\nwant: %s\ngot:  %s", step, name, want, legacy[name])
			}
		}
	}
	onlyChanged := func(step string, a, b map[string][]string, want ...string) {
		t.Helper()
		if got := changedTables(a, b); !slices.Equal(got, want) {
			t.Errorf("%s: tables changed %v, want exactly %v", step, got, want)
		}
	}

	// 2. Migration up. Nothing a sidecar receives changes, for this gateway or
	// an older one still running, and only the new tables gain rows.
	crossVersionMigrate(t, inst, crossVersionAfter)
	var s2 map[string][]string
	crossVersionSession(t, inst, func() {
		sameServed("after 000133")
		s2 = dbState(t)
		onlyChanged("after 000133", s1, s2,
			"private.ai_session_analyzer_rules_mirrors", "private.datamasking_rules_mirrors",
			"private.guardrail_rules_mirrors", "public.schema_migrations")
	})

	// 3. An older gateway unbinds g-b from appdb and g-c from reports, the
	// way it writes: the listener rows only. Both versions must then serve
	// the same thing.
	var s3 map[string][]string
	crossVersionSession(t, inst, func() {
		crossVersionExec(t, `DELETE FROM private.guardrail_rules_listeners WHERE org_id = ? AND guardrail_rule_name IN ('g-b', 'g-c')`, org)
		current, legacy := served(t, pay, legacySc)
		for name := range current {
			if current[name] != legacy[name] {
				t.Errorf("after an older gateway's unbind, %s gets different documents\nolder:   %s\ncurrent: %s",
					name, legacy[name], current[name])
			}
		}
		if strings.Contains(current["pay"], `"g-b"`) || strings.Contains(current["pay"], `"g-c"`) {
			t.Errorf("the unbound rules must be gone, got %s", current["pay"])
		}
		s3 = dbState(t)
		onlyChanged("older gateway unbind", s2, s3, "private.guardrail_rules_listeners")
	})

	// 4. This gateway's mirror writer repairs the mirror copies, and nothing a
	// sidecar receives changes.
	var s4 map[string][]string
	crossVersionSession(t, inst, func() {
		before, _ := served(t, pay, legacySc)
		if err := models.DB.Transaction(func(tx *gorm.DB) error {
			return models.SyncSidecarBindingsToMirrorsTx(tx, crossVersionOrgID, pay.ID)
		}); err != nil {
			t.Fatalf("sync: %v", err)
		}
		after, _ := served(t, pay, legacySc)
		if !maps.Equal(before, after) {
			t.Errorf("the sync changed what sidecars receive\nbefore: %v\nafter:  %v", before, after)
		}
		s4 = dbState(t)
		onlyChanged("mirror sync", s3, s4, "private.guardrail_rules_mirrors")
		var stale int
		if err := models.DB.Raw(`SELECT count(*) FROM private.guardrail_rules_mirrors
			WHERE guardrail_rule_name IN ('g-b', 'g-c')`).Scan(&stale).Error; err != nil || stale != 0 {
			t.Errorf("want no mirror copy of an unbound rule, got %d (%v)", stale, err)
		}
	})

	// 5. Migration down: an older gateway reads exactly what it read before
	// the rollback, and nothing but the new tables goes.
	crossVersionMigrate(t, inst, crossVersionBefore)
	crossVersionSession(t, inst, func() {
		s5 := dbState(t)
		onlyChanged("after down", s4, s5,
			"private.ai_session_analyzer_rules_mirrors", "private.datamasking_rules_mirrors",
			"private.guardrail_rules_mirrors", "public.schema_migrations")
		for _, sc := range []*models.Sidecar{pay, legacySc} {
			g, m, a := legacyRulesForSidecar(t, models.DB, sc.ID)
			stored, err := models.GetSidecarByNameOrID(models.DB, crossVersionOrgID, sc.ID)
			if err != nil {
				t.Fatal(err)
			}
			old, err := foldSidecarRules(daemon.Config(stored.Configuration), g, m, a)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(mustJSON(t, old), `"g-b"`) {
				t.Errorf("the rollback must not bring back a binding an older gateway removed: %s", mustJSON(t, old))
			}
		}
	})
}
