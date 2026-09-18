package models_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/daemon"
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
		{SidecarID: sc.ID, ListenerName: ""}, // the whole sidecar
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
	if bound[0].ListenerName != "" || bound[1].ListenerName != "appdb" {
		t.Errorf("want a stable order, got %q then %q", bound[0].ListenerName, bound[1].ListenerName)
	}
	// The join carries the rule's own content, which is what gets translated.
	var doc struct {
		Rules []struct {
			Type  string   `json:"type"`
			Words []string `json:"words"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(bound[0].Input, &doc); err != nil {
		t.Fatalf("the joined input is not the stored shape: %v", err)
	}
	if len(doc.Rules) != 1 || doc.Rules[0].Words[0] != "DROP TABLE" {
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
