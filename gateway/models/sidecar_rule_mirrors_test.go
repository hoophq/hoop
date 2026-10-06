package models_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

// Before 000133 and after it, the rows that bind a sidecar rule.
const (
	beforeRulesOnMirrorsVersion = 132
	rulesOnMirrorsVersion       = 133
)

// mirrorFixture is one sidecar with two listeners. appdb has a mirror and
// reports has none, as a listener in an org with beta.sidecar_listeners off.
type mirrorFixture struct {
	sc       *models.Sidecar
	mirrorID string
}

func seedMirrorFixture(t *testing.T) mirrorFixture {
	t.Helper()
	execSQL(t, `INSERT INTO private.orgs (id, name) VALUES (?, 'rules-on-mirrors') ON CONFLICT DO NOTHING`, testOrgID)
	sc := &models.Sidecar{
		OrgID: testOrgID, Name: "pay", KeyHash: models.HashAPIKey("hsc_rules_on_mirrors"),
		CreatedBy: "tests@hoop.dev",
		Configuration: models.SidecarConfiguration{Listeners: []daemon.ListenerConfig{
			{Name: "appdb", Protocol: "postgres", Listen: ":5432", Upstream: "db:5432"},
			{Name: "reports", Protocol: "postgres", Listen: ":5433", Upstream: "db:5433"},
		}},
	}
	if err := models.CreateSidecar(models.DB, sc); err != nil {
		t.Fatalf("seed sidecar: %v", err)
	}
	mirrors, err := services.ProjectListeners(testOrgID, sc)
	if err != nil {
		t.Fatalf("project listeners: %v", err)
	}
	err = models.DB.Transaction(func(tx *gorm.DB) error {
		return models.SyncSidecarConnectionsTx(tx, testOrgID, sc.ID, mirrors[:1])
	})
	if err != nil {
		t.Fatalf("seed the appdb mirror: %v", err)
	}
	var mirrorID string
	execScan(t, &mirrorID, `SELECT id FROM private.connections WHERE sidecar_id = ? AND sidecar_listener = 'appdb'`, sc.ID)
	return mirrorFixture{sc: sc, mirrorID: mirrorID}
}

// seedListenerBindings writes rules bound by listener name, as every gateway
// before 000133 stored them. Positions run against name order, so a copy that
// drops them reorders the lane.
func seedListenerBindings(t *testing.T, f mirrorFixture) {
	t.Helper()
	org := uuid.MustParse(testOrgID)
	guardrail := func(name, op string) {
		r := &models.GuardRailRules{OrgID: testOrgID, ID: uuid.NewString(), Name: name,
			Input: map[string]any{}, Output: map[string]any{},
			SidecarSpec: json.RawMessage(`{"rules":[{"name":"` + name + `","type":"operation","operations":["` + op + `"]}]}`)}
		if err := models.UpsertGuardRailRuleWithConnections(r, nil, true); err != nil {
			t.Fatalf("seed guardrail %s: %v", name, err)
		}
	}
	guardrail("g-b", "drop")
	guardrail("g-a", "truncate")
	guardrail("g-c", "delete")
	mask := &models.DataMaskingRule{ID: uuid.NewString(), OrgID: testOrgID, Name: "m-email",
		SupportedEntityTypes: models.SupportedEntityTypesList{}, CustomEntityTypes: models.CustomEntityTypesList{},
		SidecarSpec: json.RawMessage(`{"rules":[{"entities":["EMAIL_ADDRESS"],"strategy":"mask"}]}`)}
	if _, err := models.CreateDataMaskingRule(mask); err != nil {
		t.Fatalf("seed mask: %v", err)
	}
	analyzer := &models.AISessionAnalyzerRules{OrgID: org, Name: "an-deletes",
		ConnectionNames: pq.StringArray{},
		SidecarSpec:     json.RawMessage(`{"trigger":{"operations":["delete"]},"high":"deny"}`)}
	if err := models.CreateAISessionAnalyzerRule(analyzer); err != nil {
		t.Fatalf("seed analyzer: %v", err)
	}
	for _, b := range []struct {
		table, col, rule, listener string
		pos                        int
	}{
		{"guardrail_rules_listeners", "guardrail_rule_name", "g-b", "appdb", 0},
		{"guardrail_rules_listeners", "guardrail_rule_name", "g-a", "appdb", 1},
		{"guardrail_rules_listeners", "guardrail_rule_name", "g-c", "reports", 0},
		{"datamasking_rules_listeners", "datamasking_rule_name", "m-email", "appdb", 0},
		{"ai_session_analyzer_rules_listeners", "analyzer_rule_name", "an-deletes", "appdb", 0},
	} {
		execSQL(t, `INSERT INTO private.`+b.table+` (org_id, `+b.col+`, sidecar_id, listener_name, position)
			VALUES (?, ?, ?, ?, ?)`, org, b.rule, f.sc.ID, b.listener, b.pos)
	}
}

// servedDocument is the configuration the sidecar receives on its handshake.
func servedDocument(t *testing.T, sc *models.Sidecar) string {
	t.Helper()
	stored, err := models.GetSidecarByNameOrID(models.DB, testOrgID, sc.ID)
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	composed, err := services.ComposeSidecarConfiguration(models.DB, stored)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	doc, err := json.Marshal(composed)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(doc)
}

func execScan(t *testing.T, dest any, query string, args ...any) {
	t.Helper()
	if err := models.DB.Raw(query, args...).Scan(dest).Error; err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
}

// oldGatewayView is every row a gateway older than 000133 reads to compose a
// sidecar's rules: the listener tables, nothing else.
func oldGatewayView(t *testing.T) []string {
	t.Helper()
	var rows []string
	execScan(t, &rows, `
	SELECT 'guardrail/' || guardrail_rule_name || '/' || listener_name || '/' || position FROM private.guardrail_rules_listeners
	UNION ALL
	SELECT 'datamasking/' || datamasking_rule_name || '/' || listener_name || '/' || position FROM private.datamasking_rules_listeners
	UNION ALL
	SELECT 'analyzer/' || analyzer_rule_name || '/' || listener_name || '/' || position FROM private.ai_session_analyzer_rules_listeners
	ORDER BY 1`)
	return rows
}

func countRows(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	execScan(t, &n, query, args...)
	return n
}

// withSession is withDB for a test that reopens the pool on one backend. The
// embedded backend keeps one session, and with it the statements the last
// pool prepared, so a new pool preparing the same query collides with them.
func withSession(t *testing.T, inst *pglite.Instance, fn func()) {
	t.Helper()
	withDB(t, inst, func() {
		execSQL(t, `DEALLOCATE ALL`)
		fn()
	})
}

// 000133 copies every binding whose listener has a mirror onto the mirror. The
// sidecar must receive the same document before the copy, after it, and after
// the rollback.
func TestRulesOnMirrorsMigrationServesTheSameConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	ctx := context.Background()
	inst, err := pglite.Start(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("start embedded database: %v", err)
	}
	t.Cleanup(func() { inst.Close(ctx) })
	migrateTo(t, inst, func(m *migrate.Migrate) error { return m.Migrate(rulesOnMirrorsVersion) })

	var f mirrorFixture
	var before string
	var oldView []string
	withSession(t, inst, func() {
		f = seedMirrorFixture(t)
		seedListenerBindings(t, f)
		before = servedDocument(t, f.sc)
		oldView = oldGatewayView(t)
	})
	// The comparison proves nothing over a document that carries no rule.
	gb, ga := strings.Index(before, `"g-b"`), strings.Index(before, `"g-a"`)
	if gb < 0 || ga < 0 || gb > ga || !strings.Contains(before, `"g-c"`) ||
		!strings.Contains(before, "EMAIL_ADDRESS") || !strings.Contains(before, `"high":"deny"`) {
		t.Fatalf("want every seeded rule served, g-b before g-a, got %s", before)
	}

	// Back to the schema the bindings were written under, with a rule bound to
	// the mirror through its connection list: no sidecar read that row, so it
	// protected nothing.
	migrateTo(t, inst, func(m *migrate.Migrate) error { return m.Migrate(beforeRulesOnMirrorsVersion) })
	withSession(t, inst, func() {
		gw := &models.GuardRailRules{OrgID: testOrgID, ID: uuid.NewString(), Name: "gateway-only",
			Input: map[string]any{}, Output: map[string]any{}}
		if err := models.UpsertGuardRailRuleWithConnections(gw, nil, true); err != nil {
			t.Fatalf("seed gateway rule: %v", err)
		}
		execSQL(t, `INSERT INTO private.guardrail_rules_connections (org_id, rule_id, connection_id) VALUES (?, ?, ?)`,
			testOrgID, gw.ID, f.mirrorID)
	})

	migrateTo(t, inst, func(m *migrate.Migrate) error { return m.Migrate(rulesOnMirrorsVersion) })
	withSession(t, inst, func() {
		if after := servedDocument(t, f.sc); after != before {
			t.Fatalf("the served document changed with the copy\nbefore: %s\nafter:  %s", before, after)
		}
		// A gateway still on the old code, in a rolling deploy or after an
		// image rollback, reads only the listener tables. It must find every
		// rule where it was, or it serves the sidecar no rules at all.
		if got := oldGatewayView(t); !equalStrings(got, oldView) {
			t.Errorf("an older gateway reads different bindings after the copy\nbefore: %v\nafter:  %v", oldView, got)
		}
		var onMirror []struct {
			Name     string
			Position int
		}
		execScan(t, &onMirror, `SELECT r.name, g.position FROM private.guardrail_rules_connections g
			JOIN private.guardrail_rules r ON r.id = g.rule_id WHERE g.connection_id = ? ORDER BY g.position`, f.mirrorID)
		if len(onMirror) != 2 || onMirror[0].Name != "g-b" || onMirror[0].Position != 0 ||
			onMirror[1].Name != "g-a" || onMirror[1].Position != 1 {
			t.Errorf("want g-b then g-a on the mirror, in place, got %+v", onMirror)
		}
		if n := countRows(t, `SELECT count(*) FROM private.datamasking_rules_connections WHERE connection_id = ?`, f.mirrorID); n != 1 {
			t.Errorf("want the mask rule on the mirror, got %d", n)
		}
		if n := countRows(t, `SELECT count(*) FROM private.ai_session_analyzer_rules_connections WHERE connection_id = ?`, f.mirrorID); n != 1 {
			t.Errorf("want the analyzer rule on the mirror, got %d", n)
		}
	})

	migrateTo(t, inst, func(m *migrate.Migrate) error { return m.Migrate(beforeRulesOnMirrorsVersion) })
	withSession(t, inst, func() {
		if n := countRows(t, `SELECT count(*) FROM private.guardrail_rules_connections WHERE connection_id = ?`, f.mirrorID); n != 0 {
			t.Errorf("the rollback must leave no binding on the mirror, got %d", n)
		}
		var positions []int
		execScan(t, &positions, `SELECT position FROM private.guardrail_rules_listeners
			WHERE listener_name = 'appdb' ORDER BY guardrail_rule_name`)
		if len(positions) != 2 || positions[0] != 1 || positions[1] != 0 {
			t.Errorf("want g-a at 1 and g-b at 0 back on the listener, got %v", positions)
		}
		if n := countRows(t, `SELECT count(*) FROM private.ai_session_analyzer_rules_listeners WHERE listener_name = 'appdb'`); n != 1 {
			t.Errorf("want the analyzer binding back on the listener, got %d", n)
		}
	})
	migrateTo(t, inst, func(m *migrate.Migrate) error { return m.Migrate(rulesOnMirrorsVersion) })
	withSession(t, inst, func() {
		if after := servedDocument(t, f.sc); after != before {
			t.Fatalf("the served document changed after a rollback and a new copy\nbefore: %s\nafter:  %s", before, after)
		}
		// An older gateway removes g-b after the migration: it deletes the
		// listener row only.
		execSQL(t, `DELETE FROM private.guardrail_rules_listeners WHERE guardrail_rule_name = 'g-b'`)
		oldView = oldGatewayView(t)
	})
	// The rollback must not bring the removed binding back.
	migrateTo(t, inst, func(m *migrate.Migrate) error { return m.Migrate(beforeRulesOnMirrorsVersion) })
	withSession(t, inst, func() {
		if got := oldGatewayView(t); !equalStrings(got, oldView) {
			t.Errorf("the rollback changed what an older gateway reads\nwant: %v\ngot:  %v", oldView, got)
		}
	})
}

// During a rolling deploy an older gateway writes only the listener rows. Its
// changes must count here at once, and the mirror writer repairs the mirror.
func TestAnOlderGatewayWriteCounts(t *testing.T) {
	startTestDB(t)
	f := seedMirrorFixture(t)
	seedListenerBindings(t, f)
	featureflag.Set(testOrgID, featureflag.FlagSidecarListeners, true)
	t.Cleanup(func() { featureflag.Set(testOrgID, featureflag.FlagSidecarListeners, false) })
	sync := func() {
		t.Helper()
		err := models.DB.Transaction(func(tx *gorm.DB) error {
			return services.SyncSidecarListenerConnectionsTx(tx, f.sc)
		})
		if err != nil {
			t.Fatalf("sync mirrors: %v", err)
		}
	}
	sync()
	onAppdb := func() int {
		return countRows(t, `SELECT count(*) FROM private.guardrail_rules_connections WHERE connection_id = ?`, f.mirrorID)
	}
	if n := onAppdb(); n != 2 {
		t.Fatalf("want g-b and g-a on the appdb mirror, got %d", n)
	}

	// The older gateway unbinds g-b and binds g-c to appdb.
	execSQL(t, `DELETE FROM private.guardrail_rules_listeners WHERE guardrail_rule_name = 'g-b'`)
	execSQL(t, `INSERT INTO private.guardrail_rules_listeners (org_id, guardrail_rule_name, sidecar_id, listener_name, position)
		VALUES (?, 'g-c', ?, 'appdb', 5)`, testOrgID, f.sc.ID)

	doc := servedDocument(t, f.sc)
	lane := composedLane(t, f.sc, "appdb")
	if lane.Guardrails == nil || len(lane.Guardrails.Rules) != 2 || strings.Contains(doc, `"g-b"`) {
		t.Errorf("want g-a and g-c served on appdb and g-b gone, got %s", doc)
	}
	targets, err := models.ListGuardrailRuleTargets(models.DB, uuid.MustParse(testOrgID), "g-b")
	if err != nil {
		t.Fatalf("list targets: %v", err)
	}
	if len(targets) != 0 {
		t.Errorf("want g-b bound nowhere, got %+v", targets)
	}

	sync()
	var onMirror []struct {
		Name     string
		Position int
	}
	execScan(t, &onMirror, `SELECT r.name, g.position FROM private.guardrail_rules_connections g
		JOIN private.guardrail_rules r ON r.id = g.rule_id WHERE g.connection_id = ? ORDER BY g.position`, f.mirrorID)
	if len(onMirror) != 2 || onMirror[0].Name != "g-a" || onMirror[1].Name != "g-c" || onMirror[1].Position != 5 {
		t.Errorf("want the mirror to follow the listener rows, g-a then g-c at 5, got %+v", onMirror)
	}
}

// A mirror made after 000133 (a fallback name, an org that turns the flag on)
// takes the bindings of its listener when the mirror writer runs.
func TestTheMirrorWriterCopiesBindingsOntoANewMirror(t *testing.T) {
	startTestDB(t)
	f := seedMirrorFixture(t)
	seedListenerBindings(t, f)
	before := servedDocument(t, f.sc)

	featureflag.Set(testOrgID, featureflag.FlagSidecarListeners, true)
	t.Cleanup(func() { featureflag.Set(testOrgID, featureflag.FlagSidecarListeners, false) })
	err := models.DB.Transaction(func(tx *gorm.DB) error {
		return services.SyncSidecarListenerConnectionsTx(tx, f.sc)
	})
	if err != nil {
		t.Fatalf("sync mirrors: %v", err)
	}

	if after := servedDocument(t, f.sc); after != before {
		t.Fatalf("the served document changed with the copy\nbefore: %s\nafter:  %s", before, after)
	}
	if n := countRows(t, `SELECT count(*) FROM private.guardrail_rules_connections g
		JOIN private.connections c ON c.id = g.connection_id WHERE c.sidecar_listener = 'reports'`); n != 1 {
		t.Errorf("want g-c copied onto the new reports mirror, got %d", n)
	}
	if got := oldGatewayView(t); len(got) != 5 {
		t.Errorf("the listener rows stay for older gateways, got %v", got)
	}
	targets, err := models.ListGuardrailRuleTargets(models.DB, uuid.MustParse(testOrgID), "g-c")
	if err != nil {
		t.Fatalf("list targets: %v", err)
	}
	if len(targets) != 1 || targets[0].SidecarID != f.sc.ID || targets[0].ListenerName != "reports" {
		t.Errorf("a target reads back as its sidecar and listener wherever it is stored, got %+v", targets)
	}
}

// A sidecar target on a listener with a mirror is stored on the mirror, and a
// rule's connection list neither shows nor changes that row.
func TestASidecarTargetIsStoredOnTheMirror(t *testing.T) {
	startTestDB(t)
	f := seedMirrorFixture(t)
	org := uuid.MustParse(testOrgID)
	rule := &models.GuardRailRules{OrgID: testOrgID, ID: uuid.NewString(), Name: "no-drop",
		Input: map[string]any{}, Output: map[string]any{},
		SidecarSpec: json.RawMessage(`{"rules":[{"name":"no-drop","type":"operation","operations":["drop"]}]}`)}
	if err := models.UpsertGuardRailRuleWithConnections(rule, nil, true); err != nil {
		t.Fatalf("seed rule: %v", err)
	}
	err := models.SetGuardrailRuleListeners(models.DB, org, rule.Name, []models.SidecarRuleTarget{
		{SidecarID: f.sc.ID, ListenerName: "appdb"},
		{SidecarID: f.sc.ID, ListenerName: "reports"},
	})
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if n := countRows(t, `SELECT count(*) FROM private.guardrail_rules_connections WHERE connection_id = ?`, f.mirrorID); n != 1 {
		t.Fatalf("want the appdb binding on its mirror, got %d", n)
	}
	if n := countRows(t, `SELECT count(*) FROM private.guardrail_rules_listeners WHERE listener_name = 'reports'`); n != 1 {
		t.Fatalf("want the reports binding on its listener, got %d", n)
	}
	// Dual write: an older gateway reads the appdb binding from the listener.
	if n := countRows(t, `SELECT count(*) FROM private.guardrail_rules_listeners WHERE listener_name = 'appdb'`); n != 1 {
		t.Fatalf("want the appdb binding on its listener too, got %d", n)
	}
	if lane := composedLane(t, f.sc, "appdb"); lane.Guardrails == nil || len(lane.Guardrails.Rules) != 1 {
		t.Errorf("want the rule served on appdb, got %+v", lane.Guardrails)
	}

	stored, err := models.GetGuardRailRules(testOrgID, rule.ID)
	if err != nil {
		t.Fatalf("read rule: %v", err)
	}
	if len(stored.ConnectionIDs) != 0 {
		t.Errorf("a binding on a mirror is a sidecar target, not a connection, got %v", stored.ConnectionIDs)
	}
	// An edit that sends the connection list it read keeps the binding.
	if err := models.UpsertGuardRailRuleWithConnections(rule, stored.ConnectionIDs, false); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if n := countRows(t, `SELECT count(*) FROM private.guardrail_rules_connections WHERE connection_id = ?`, f.mirrorID); n != 1 {
		t.Errorf("an edit of the connection list must leave the mirror binding, got %d", n)
	}
	// Binding a mirror through the connection list would skip the sidecar
	// write checks.
	err = models.UpsertGuardRailRuleWithConnections(rule, []string{f.mirrorID}, false)
	if !errors.Is(err, models.ErrSidecarMirrorRuleBinding) {
		t.Errorf("want ErrSidecarMirrorRuleBinding, got %v", err)
	}
	mask := &models.DataMaskingRule{ID: uuid.NewString(), OrgID: testOrgID, Name: "mask-on-mirror",
		SupportedEntityTypes: models.SupportedEntityTypesList{}, CustomEntityTypes: models.CustomEntityTypesList{},
		ConnectionIDs: pq.StringArray{f.mirrorID}}
	if _, err := models.CreateDataMaskingRule(mask); !errors.Is(err, models.ErrSidecarMirrorRuleBinding) {
		t.Errorf("want ErrSidecarMirrorRuleBinding for a mask rule, got %v", err)
	}

	// The connection side: sending the mirror's own list back is a no-op, and
	// a different list is refused.
	_, err = models.UpdateDataMaskingRuleConnection(testOrgID, f.mirrorID, []models.DataMaskingRuleConnection{
		{ID: uuid.NewString(), OrgID: testOrgID, RuleID: uuid.NewString(), ConnectionID: f.mirrorID, Status: "active"},
	})
	if !errors.Is(err, models.ErrSidecarMirrorRuleBinding) {
		t.Errorf("want ErrSidecarMirrorRuleBinding from the connection side, got %v", err)
	}
	if _, err := models.UpdateDataMaskingRuleConnection(testOrgID, f.mirrorID, nil); err != nil {
		t.Errorf("the mirror's own (empty) mask list must pass, got %v", err)
	}
}

// A rule bound on another sidecar's mirror survives the detach of this one.
func TestADetachKeepsARuleBoundOnAnotherMirror(t *testing.T) {
	startTestDB(t)
	f := seedMirrorFixture(t)
	seedListenerBindings(t, f)
	other := &models.Sidecar{OrgID: testOrgID, Name: "other", KeyHash: models.HashAPIKey("hsc_other_mirror"),
		CreatedBy: "tests@hoop.dev",
		Configuration: models.SidecarConfiguration{Listeners: []daemon.ListenerConfig{
			{Name: "appdb", Protocol: "postgres", Listen: ":5432", Upstream: "db:5432"},
		}}}
	if err := models.CreateSidecar(models.DB, other); err != nil {
		t.Fatalf("seed other sidecar: %v", err)
	}
	mirrors, err := services.ProjectListeners(testOrgID, other)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	err = models.DB.Transaction(func(tx *gorm.DB) error {
		return models.SyncSidecarConnectionsTx(tx, testOrgID, other.ID, mirrors)
	})
	if err != nil {
		t.Fatalf("mirror other: %v", err)
	}
	org := uuid.MustParse(testOrgID)
	err = models.SetGuardrailRuleListeners(models.DB, org, "g-b", []models.SidecarRuleTarget{
		{SidecarID: f.sc.ID, ListenerName: "appdb"}, {SidecarID: other.ID, ListenerName: "appdb"},
	})
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	for _, name := range []string{"g-b", "g-a", "g-c", "m-email", "an-deletes"} {
		execSQL(t, `UPDATE private.guardrail_rules SET imported_from_sidecar = ? WHERE name = ?`, f.sc.ID, name)
	}

	var detached models.DetachedSidecarRules
	err = models.DB.Transaction(func(tx *gorm.DB) error {
		var derr error
		detached, derr = models.DetachSidecarRulesTx(tx, org, f.sc.ID)
		return derr
	})
	if err != nil {
		t.Fatalf("detach: %v", err)
	}
	if want := []string{"g-a", "g-c"}; !equalStrings(detached.Guardrails, want) {
		t.Errorf("want the imported rules bound nowhere else deleted %v, got %v", want, detached.Guardrails)
	}
	if want := []string{"g-b"}; !equalStrings(detached.Unbound.Guardrails, want) {
		t.Errorf("want g-b only unbound, got %v", detached.Unbound.Guardrails)
	}
	if want := []string{"an-deletes"}; !equalStrings(detached.Unbound.Analyzers, want) {
		t.Errorf("want the analyzer unbound from the mirror, got %v", detached.Unbound.Analyzers)
	}
	if bound, _ := models.ListSidecarRuleBindings(models.DB, org, f.sc.ID); len(bound) != 0 {
		t.Errorf("want nothing left on the detached sidecar, got %+v", bound)
	}
	if bound, _ := models.ListSidecarRuleBindings(models.DB, org, other.ID); len(bound) != 1 {
		t.Errorf("want g-b left on the other sidecar, got %+v", bound)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
