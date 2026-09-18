package models_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/lib/pq"
)

// TestGuardrailRuleListeners covers the binding end to end against a real
// schema: the migration, the foreign keys, the replace-set write and the join
// composition reads.
//
// The join is the one that matters. If it is wrong, a rule an admin bound is
// simply absent from the served document, and nothing anywhere reports it: the
// sidecar enforces less than the fleet view claims.
func TestGuardrailRuleListeners(t *testing.T) {
	startTestDB(t)
	orgID := uuid.MustParse(testOrgID)

	sc := &models.Sidecar{
		OrgID:     testOrgID,
		Name:      "bind-target",
		KeyHash:   models.HashAPIKey("hsc_bind_target_test"),
		CreatedBy: "tests@hoop.dev",
		Configuration: models.SidecarConfiguration{
			Listeners: []daemon.ListenerConfig{{
				Name: "appdb", Protocol: "postgres", Listen: ":5432", Upstream: "db:5432",
			}},
		},
	}
	if err := models.CreateSidecar(models.DB, sc); err != nil {
		t.Fatalf("seed sidecar: %v", err)
	}

	rule := &models.GuardRailRules{
		OrgID: testOrgID,
		ID:    uuid.NewString(),
		Name:  "no-drop",
		Input: map[string]any{"rules": []any{map[string]any{
			"type": "deny_words_list", "words": []any{"DROP TABLE"},
		}}},
		Output: map[string]any{"rules": []any{}},
		// The sidecar's half, in the sidecar's own vocabulary. It is what the
		// join returns, and what composition places on a lane.
		SidecarSpec: json.RawMessage(
			`{"rules":[{"name":"no-drop","type":"operation","operations":["drop"]}]}`),
	}
	if err := models.UpsertGuardRailRuleWithConnections(rule, nil, true); err != nil {
		t.Fatalf("seed guardrail rule: %v", err)
	}

	// Nothing bound yet: composition must find nothing rather than everything.
	bound, err := models.ListGuardrailRulesForSidecar(models.DB, orgID, sc.ID)
	if err != nil {
		t.Fatalf("list before binding: %v", err)
	}
	if len(bound) != 0 {
		t.Fatalf("an unbound rule must not reach a sidecar, got %d", len(bound))
	}

	targets := []models.SidecarRuleTarget{
		{SidecarID: sc.ID, ListenerName: "appdb"},
		{SidecarID: sc.ID, ListenerName: "reporting"},
	}
	if err := models.SetGuardrailRuleListeners(models.DB, orgID, rule.Name, targets); err != nil {
		t.Fatalf("bind: %v", err)
	}

	bound, err = models.ListGuardrailRulesForSidecar(models.DB, orgID, sc.ID)
	if err != nil {
		t.Fatalf("list after binding: %v", err)
	}
	if len(bound) != 2 {
		t.Fatalf("want both targets, got %d: %+v", len(bound), bound)
	}
	// Ordered, because the served document is hashed into a revision and an
	// unstable order would report every sidecar as lagging forever.
	if bound[0].ListenerName != "appdb" || bound[1].ListenerName != "reporting" {
		t.Errorf("want a stable order, got %q then %q", bound[0].ListenerName, bound[1].ListenerName)
	}
	// The join carries the SIDECAR's block, not the gateway's columns: that is
	// the document the listener receives, and composition places it as is.
	var doc struct {
		Rules []struct {
			Type       string   `json:"type"`
			Operations []string `json:"operations"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(bound[0].Spec, &doc); err != nil {
		t.Fatalf("the joined spec is not the stored shape: %v", err)
	}
	if len(doc.Rules) != 1 || doc.Rules[0].Type != "operation" || doc.Rules[0].Operations[0] != "drop" {
		t.Errorf("the rule content did not survive the join: %+v", doc.Rules)
	}

	// The write is a replace-set: a target the admin removed must disappear,
	// not linger and keep being enforced.
	err = models.SetGuardrailRuleListeners(models.DB, orgID, rule.Name,
		[]models.SidecarRuleTarget{{SidecarID: sc.ID, ListenerName: "appdb"}})
	if err != nil {
		t.Fatalf("rebind: %v", err)
	}
	bound, _ = models.ListGuardrailRulesForSidecar(models.DB, orgID, sc.ID)
	if len(bound) != 1 || bound[0].ListenerName != "appdb" {
		t.Fatalf("want only the remaining target, got %+v", bound)
	}

	// Deleting the sidecar drops its bindings: that is the foreign key, and
	// without it a rule would keep naming a sidecar that no longer exists.
	if _, err := models.DeleteSidecarByNameOrID(models.DB, testOrgID, sc.Name); err != nil {
		t.Fatalf("delete sidecar: %v", err)
	}
	left, err := models.ListGuardrailRuleTargets(models.DB, orgID, rule.Name)
	if err != nil {
		t.Fatalf("list targets after delete: %v", err)
	}
	if len(left) != 0 {
		t.Errorf("deleting the sidecar must drop its bindings, got %+v", left)
	}
}

// A rule bound to one sidecar must not compose onto another.
//
// Two sidecars in one organization is the normal case, and the composition
// query is the only thing keeping their documents apart: a rule that leaked
// would enforce on a lane nobody bound it to, and the operator reading the
// rule's own page would see the right listener the whole time.
func TestARuleBoundToOneSidecarDoesNotReachAnother(t *testing.T) {
	startTestDB(t)
	orgID := uuid.MustParse(testOrgID)

	first := &models.Sidecar{
		OrgID: testOrgID, Name: "first", KeyHash: models.HashAPIKey("hsc_first_test"),
		CreatedBy: "tests@hoop.dev",
		Configuration: models.SidecarConfiguration{Listeners: []daemon.ListenerConfig{{
			Name: "appdb", Protocol: "postgres", Listen: ":5432", Upstream: "db:5432",
		}}},
	}
	second := &models.Sidecar{
		OrgID: testOrgID, Name: "second", KeyHash: models.HashAPIKey("hsc_second_test"),
		CreatedBy: "tests@hoop.dev",
		Configuration: models.SidecarConfiguration{Listeners: []daemon.ListenerConfig{{
			Name: "warehouse", Protocol: "postgres", Listen: ":5433", Upstream: "db:5433",
		}}},
	}
	for _, sc := range []*models.Sidecar{first, second} {
		if err := models.CreateSidecar(models.DB, sc); err != nil {
			t.Fatalf("seed sidecar %s: %v", sc.Name, err)
		}
	}

	rule := &models.GuardRailRules{
		OrgID: testOrgID, ID: uuid.NewString(), Name: "only-on-second",
		Input: map[string]any{"rules": []any{}}, Output: map[string]any{"rules": []any{}},
		SidecarSpec: json.RawMessage(`{"rules":[{"name":"r","type":"operation","operations":["drop"]}]}`),
	}
	if err := models.UpsertGuardRailRuleWithConnections(rule, nil, true); err != nil {
		t.Fatalf("seed guardrail rule: %v", err)
	}
	err := models.SetGuardrailRuleListeners(models.DB, orgID, rule.Name,
		[]models.SidecarRuleTarget{{SidecarID: second.ID, ListenerName: "warehouse"}})
	if err != nil {
		t.Fatalf("bind to the second sidecar: %v", err)
	}

	onSecond, err := models.ListGuardrailRulesForSidecar(models.DB, orgID, second.ID)
	if err != nil {
		t.Fatalf("list on second: %v", err)
	}
	if len(onSecond) != 1 || onSecond[0].ListenerName != "warehouse" {
		t.Fatalf("the rule did not reach the sidecar it was bound to: %+v", onSecond)
	}

	onFirst, err := models.ListGuardrailRulesForSidecar(models.DB, orgID, first.ID)
	if err != nil {
		t.Fatalf("list on first: %v", err)
	}
	if len(onFirst) != 0 {
		t.Fatalf("a rule bound to %q leaked onto %q: %+v", second.Name, first.Name, onFirst)
	}

	// The composed documents say the same thing, which is what the handshake
	// actually serves.
	composedFirst, err := services.ComposeSidecarConfiguration(models.DB, first)
	if err != nil {
		t.Fatalf("compose first: %v", err)
	}
	if g := composedFirst.Listeners[0].Guardrails; g != nil && len(g.Rules) != 0 {
		t.Errorf("the first sidecar was served rules it has none bound: %+v", g.Rules)
	}
	composedSecond, err := services.ComposeSidecarConfiguration(models.DB, second)
	if err != nil {
		t.Fatalf("compose second: %v", err)
	}
	if g := composedSecond.Listeners[0].Guardrails; g == nil || len(g.Rules) != 1 {
		t.Errorf("the second sidecar was not served its own rule: %+v", g)
	}
}

// A masking rule's sidecar_spec has to survive a read.
//
// The datamasking reads name their columns one by one, unlike the guardrail and
// analyzer ones which go through GORM's struct mapping, so a column added to
// the table is absent from the answer until someone adds it to the list too.
// The rule then opens in the editor with its name, its description and its
// listeners intact and its content gone — and saving from there overwrites the
// stored rule with the empty one.
func TestADataMaskingRuleKeepsItsSidecarSpecOnRead(t *testing.T) {
	startTestDB(t)

	const spec = `{"rules":[{"name":"ssn-column","columns":["ssn"],"strategy":"hash"}]}`
	created, err := models.CreateDataMaskingRule(&models.DataMaskingRule{
		ID:          uuid.NewString(),
		OrgID:       testOrgID,
		Name:        "spec-round-trip",
		Description: "keeps its content",
		SidecarSpec: json.RawMessage(spec),
	})
	if err != nil {
		t.Fatalf("seed masking rule: %v", err)
	}

	byID, err := models.GetDataMaskingRuleByID(testOrgID, created.ID)
	if err != nil {
		t.Fatalf("read by id: %v", err)
	}
	if len(byID.SidecarSpec) == 0 {
		t.Error("the rule came back with no sidecar_spec; the editor would open empty")
	} else if !strings.Contains(string(byID.SidecarSpec), "ssn-column") {
		t.Errorf("the spec did not survive the read: %s", byID.SidecarSpec)
	}

	// The list feeds the rules page, which is where an admin decides whether a
	// rule is configured at all.
	all, err := models.ListDataMaskingRules(testOrgID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, r := range all {
		if r.Name != "spec-round-trip" {
			continue
		}
		found = true
		if !strings.Contains(string(r.SidecarSpec), "ssn-column") {
			t.Errorf("the listed rule lost its spec: %s", r.SidecarSpec)
		}
	}
	if !found {
		t.Fatal("the seeded rule is missing from the listing")
	}
}

// The admin pages read the bindings, because the stored configuration does not
// carry them: composition folds a rule into the SERVED document and stores
// nothing. Without this query a listener enforcing a distributed rule renders
// as "No rules. Everything passes.", which is the control plane lying about
// what its own fleet enforces.
func TestListSidecarRuleBindings(t *testing.T) {
	startTestDB(t)
	orgID := uuid.MustParse(testOrgID)

	sc := &models.Sidecar{
		OrgID:     testOrgID,
		Name:      "read-side",
		KeyHash:   models.HashAPIKey("hsc_read_side_test"),
		CreatedBy: "tests@hoop.dev",
		Configuration: models.SidecarConfiguration{
			Listeners: []daemon.ListenerConfig{{
				Name: "appdb", Protocol: "postgres", Listen: ":5432", Upstream: "db:5432",
			}},
		},
	}
	if err := models.CreateSidecar(models.DB, sc); err != nil {
		t.Fatalf("seed sidecar: %v", err)
	}
	rule := &models.GuardRailRules{
		OrgID: testOrgID, ID: uuid.NewString(), Name: "read-side-rule",
		Input: map[string]any{"rules": []any{}}, Output: map[string]any{"rules": []any{}},
		SidecarSpec: json.RawMessage(`{"rules":[{"name":"r","type":"operation","operations":["drop"]}]}`),
	}
	if err := models.UpsertGuardRailRuleWithConnections(rule, nil, true); err != nil {
		t.Fatalf("seed guardrail rule: %v", err)
	}
	err := models.SetGuardrailRuleListeners(models.DB, orgID, rule.Name,
		[]models.SidecarRuleTarget{{SidecarID: sc.ID, ListenerName: "appdb"}})
	if err != nil {
		t.Fatalf("bind: %v", err)
	}

	// Org-wide: what the sidecars list page reads in one call.
	all, err := models.ListSidecarRuleBindings(models.DB, orgID, "")
	if err != nil {
		t.Fatalf("list org bindings: %v", err)
	}
	found := false
	for _, b := range all {
		if b.SidecarID == sc.ID && b.RuleName == rule.Name {
			found = true
			if b.Kind != "guardrail" || b.ListenerName != "appdb" {
				t.Errorf("wrong binding shape: %+v", b)
			}
		}
	}
	if !found {
		t.Fatalf("the bound rule is missing from the org listing: %+v", all)
	}

	// Scoped to one sidecar: what the detail page reads.
	one, err := models.ListSidecarRuleBindings(models.DB, orgID, sc.ID)
	if err != nil {
		t.Fatalf("list sidecar bindings: %v", err)
	}
	if len(one) != 1 || one[0].RuleName != rule.Name {
		t.Fatalf("want the one binding for this sidecar, got %+v", one)
	}

	// Another sidecar's id must not pick it up: the filter is the whole reason
	// the list page can render every row from one query.
	other, err := models.ListSidecarRuleBindings(models.DB, orgID, uuid.NewString())
	if err != nil {
		t.Fatalf("list other sidecar bindings: %v", err)
	}
	if len(other) != 0 {
		t.Errorf("a binding leaked across sidecars: %+v", other)
	}
}

// Editing a bound rule must not count it twice.
//
// The free tier allows one guardrail rule per process. The cap check composes
// what the served document WOULD be, and the rule being edited is already in
// that document under its stored name -- so folding the incoming version in
// beside it reports two rules and refuses an edit that changes no count at all.
// On the free tier that makes a bound rule permanently uneditable.
func TestEditingABoundRuleIsNotCountedTwice(t *testing.T) {
	startTestDB(t)
	orgID := uuid.MustParse(testOrgID)

	sc := &models.Sidecar{
		OrgID:     testOrgID,
		Name:      "cap-target",
		KeyHash:   models.HashAPIKey("hsc_cap_target_test"),
		CreatedBy: "tests@hoop.dev",
		Configuration: models.SidecarConfiguration{
			Listeners: []daemon.ListenerConfig{{
				Name: "appdb", Protocol: "postgres", Listen: ":5432", Upstream: "db:5432",
			}},
		},
	}
	if err := models.CreateSidecar(models.DB, sc); err != nil {
		t.Fatalf("seed sidecar: %v", err)
	}

	const spec = `{"rules":[{"name":"no-drop","type":"operation","operations":[%q]}]}`
	rule := &models.GuardRailRules{
		OrgID:       testOrgID,
		ID:          uuid.NewString(),
		Name:        "no-drop",
		Input:       map[string]any{"rules": []any{}},
		Output:      map[string]any{"rules": []any{}},
		SidecarSpec: json.RawMessage(fmt.Sprintf(spec, "drop")),
	}
	if err := models.UpsertGuardRailRuleWithConnections(rule, nil, true); err != nil {
		t.Fatalf("seed guardrail rule: %v", err)
	}
	targets := []models.SidecarRuleTarget{{SidecarID: sc.ID, ListenerName: "appdb"}}
	if err := models.SetGuardrailRuleListeners(models.DB, orgID, rule.Name, targets); err != nil {
		t.Fatalf("bind: %v", err)
	}

	// The same rule, edited. No license, so the cap is one rule per process.
	err := services.ValidateSidecarRuleTargets(models.DB, testOrgID,
		services.SidecarRuleGuardrail, rule.Name, rule.Name,
		json.RawMessage(fmt.Sprintf(spec, "truncate")), targets)
	if err != nil {
		t.Fatalf("editing the one bound rule was refused at the cap: %v", err)
	}

	// A rename still finds the stored version, which sits under the old name
	// until the write cascades it.
	err = services.ValidateSidecarRuleTargets(models.DB, testOrgID,
		services.SidecarRuleGuardrail, "no-drop-v2", rule.Name,
		json.RawMessage(fmt.Sprintf(spec, "truncate")), targets)
	if err != nil {
		t.Fatalf("renaming the one bound rule was refused at the cap: %v", err)
	}

	// The cap itself still holds: a SECOND rule on the same lane is over it.
	second := json.RawMessage(`{"rules":[{"name":"no-delete","type":"operation","operations":["delete"]}]}`)
	err = services.ValidateSidecarRuleTargets(models.DB, testOrgID,
		services.SidecarRuleGuardrail, "no-delete", "", second, targets)
	if err == nil {
		t.Fatal("a second guardrail rule on the free tier must be refused")
	}
	if !strings.Contains(err.Error(), "rule limit") {
		t.Errorf("the refusal must name the cap, got: %v", err)
	}
}

// TestASecondAnalyzerRuleOnOneListenerIsRefusedAtTheWrite pins the refusal at
// the write rather than at the handshake.
//
// A listener runs ONE analyzer block, so composition cannot build a document
// with two -- and it fails for the WHOLE sidecar, not for the rule. Accepted
// here, the save answers 200, the next handshake answers an error, every other
// rule on every other lane stops being delivered, and the fleet sits on its
// last good document until somebody thinks to look at a binding.
func TestASecondAnalyzerRuleOnOneListenerIsRefusedAtTheWrite(t *testing.T) {
	startTestDB(t)
	orgID := uuid.MustParse(testOrgID)

	sc := &models.Sidecar{
		OrgID:     testOrgID,
		Name:      "analyzer-conflict",
		KeyHash:   models.HashAPIKey("hsc_analyzer_conflict_test"),
		CreatedBy: "tests@hoop.dev",
		Configuration: models.SidecarConfiguration{
			Listeners: []daemon.ListenerConfig{{
				Name: "appdb", Protocol: "postgres", Listen: ":5432", Upstream: "db:5432",
				Analyzer: &daemon.LaneAnalyzerConfig{MaxCalls: 40},
			}},
		},
	}
	if err := models.CreateSidecar(models.DB, sc); err != nil {
		t.Fatalf("seed sidecar: %v", err)
	}

	const spec = `{"trigger":{"operations":["insert","update","delete"]},"high":"block"}`
	first := &models.AISessionAnalyzerRules{
		OrgID: orgID, Name: "risk-a", ConnectionNames: pq.StringArray{},
		SidecarSpec: json.RawMessage(spec),
	}
	if err := models.CreateAISessionAnalyzerRule(first); err != nil {
		t.Fatalf("seed analyzer rule: %v", err)
	}
	targets := []models.SidecarRuleTarget{{SidecarID: sc.ID, ListenerName: "appdb"}}
	if err := models.SetAnalyzerRuleListeners(models.DB, orgID, first.Name, targets); err != nil {
		t.Fatalf("bind: %v", err)
	}

	// Editing the rule that already owns the lane is not a conflict with
	// itself: the stored version is left out and the incoming one takes its
	// place.
	err := services.ValidateSidecarRuleTargets(models.DB, testOrgID,
		services.SidecarRuleAnalyzer, first.Name, first.Name, json.RawMessage(spec), targets)
	if err != nil {
		t.Fatalf("editing the rule that owns the lane was refused: %v", err)
	}

	// A DIFFERENT rule on the same lane is the conflict.
	err = services.ValidateSidecarRuleTargets(models.DB, testOrgID,
		services.SidecarRuleAnalyzer, "risk-b", "", json.RawMessage(spec), targets)
	if err == nil {
		t.Fatal("a second analyzer rule on one listener must be refused at the write")
	}
	if !strings.Contains(err.Error(), "risk-a") || !strings.Contains(err.Error(), "one analyzer block") {
		t.Errorf("the refusal must name the rule that already owns the lane, got: %v", err)
	}
}

// TestAConfigurationEditCannotOrphanABoundRule pins the other half of the same
// failure: the listener moving out from under the binding.
//
// Listener names ARE the binding key, so dropping a bound lane, or renaming
// it, breaks the next handshake for the whole sidecar exactly as a conflicting
// rule does -- and from a screen that never mentions rules.
func TestAConfigurationEditCannotOrphanABoundRule(t *testing.T) {
	startTestDB(t)
	orgID := uuid.MustParse(testOrgID)

	sc := &models.Sidecar{
		OrgID:     testOrgID,
		Name:      "listener-edit",
		KeyHash:   models.HashAPIKey("hsc_listener_edit_test"),
		CreatedBy: "tests@hoop.dev",
		Configuration: models.SidecarConfiguration{
			Listeners: []daemon.ListenerConfig{
				{Name: "appdb", Protocol: "postgres", Listen: ":5432", Upstream: "db:5432"},
				{Name: "reporting", Protocol: "mysql", Listen: ":3306", Upstream: "dw:3306"},
			},
		},
	}
	if err := models.CreateSidecar(models.DB, sc); err != nil {
		t.Fatalf("seed sidecar: %v", err)
	}
	rule := &models.GuardRailRules{
		OrgID:  testOrgID,
		ID:     uuid.NewString(),
		Name:   "no-drop",
		Input:  map[string]any{"rules": []any{}},
		Output: map[string]any{"rules": []any{}},
		SidecarSpec: json.RawMessage(
			`{"rules":[{"name":"no-drop","type":"table","tables":["customers"],"access":"write"}]}`),
	}
	if err := models.UpsertGuardRailRuleWithConnections(rule, nil, true); err != nil {
		t.Fatalf("seed guardrail rule: %v", err)
	}
	err := models.SetGuardrailRuleListeners(models.DB, orgID, rule.Name,
		[]models.SidecarRuleTarget{{SidecarID: sc.ID, ListenerName: "appdb"}})
	if err != nil {
		t.Fatalf("bind: %v", err)
	}

	// The configuration as stored still carries the lane: nothing to refuse.
	if err := services.ValidateSidecarBindingsForConfiguration(models.DB, sc); err != nil {
		t.Fatalf("the stored configuration was refused: %v", err)
	}

	// The same edit an admin makes when they rename or retire a listener.
	edited := *sc
	edited.Configuration = models.SidecarConfiguration{
		Listeners: []daemon.ListenerConfig{
			{Name: "appdb-v2", Protocol: "postgres", Listen: ":5432", Upstream: "db:5432"},
			{Name: "reporting", Protocol: "mysql", Listen: ":3306", Upstream: "dw:3306"},
		},
	}
	err = services.ValidateSidecarBindingsForConfiguration(models.DB, &edited)
	if err == nil {
		t.Fatal("dropping a bound listener must be refused at the write")
	}
	var broken services.ErrSidecarBindingBroken
	if !errors.As(err, &broken) {
		t.Fatalf("want a binding refusal the API answers 422, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "no-drop") || !strings.Contains(err.Error(), "appdb") {
		t.Errorf("the refusal must name the rule and the listener, got: %v", err)
	}

	// A protocol change is the quieter version: the lane is still there and
	// still named, and the rule it carries is one an ssh lane cannot read.
	reprotocoled := *sc
	reprotocoled.Configuration = models.SidecarConfiguration{
		Listeners: []daemon.ListenerConfig{
			{Name: "appdb", Protocol: "ssh", Listen: ":2222", Upstream: "host:22",
				SSH: &daemon.SSHConfig{HostKey: "/etc/hoop/hostkey", TrustedCA: "/etc/hoop/ca.pub"}},
		},
	}
	// An ssh lane reads a byte stream, not a parsed statement, so it has no
	// table to match: the sidecar refuses a `table` rule there at STARTUP.
	if err := services.ValidateSidecarBindingsForConfiguration(models.DB, &reprotocoled); err == nil {
		t.Error("an ssh lane carrying a table guardrail must be refused at the write")
	}
}
