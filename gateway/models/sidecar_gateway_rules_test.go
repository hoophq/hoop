package models_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/policy"
	"gorm.io/gorm"
)

// mirroredSidecar is a sidecar with one postgres listener and its mirror
// connection written, the way the sidecar API writes them.
func mirroredSidecar(t *testing.T, name, doc string) (*models.Sidecar, *models.SidecarMirror) {
	t.Helper()
	sc := newTestSidecar(t, name)
	cfg, err := services.ParseSidecarConfiguration([]byte(doc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	stored, err := models.UpdateSidecarConfiguration(models.DB, testOrgID, sc.ID, models.SidecarConfiguration(cfg))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	err = models.DB.Transaction(func(tx *gorm.DB) error {
		return services.SyncSidecarListenerConnectionsTx(tx, stored)
	})
	if err != nil {
		t.Fatalf("mirror: %v", err)
	}
	mirror, err := models.GetSidecarMirror(models.DB, testOrgID, sc.ID, "appdb")
	if err != nil {
		t.Fatalf("read mirror: %v", err)
	}
	return stored, mirror
}

const mirroredDoc = `{"listeners":[{"name":"appdb","protocol":"postgres","listen":":5432","upstream":"db:5432",
	"guardrails":{"rules":[{"name":"native","type":"operation","operations":["truncate"]}]}}]}`

func gatewayGuardrail(t *testing.T, name string, input, output map[string]any, connectionIDs []string, attributes []string) *models.GuardRailRules {
	t.Helper()
	rule := &models.GuardRailRules{OrgID: testOrgID, ID: uuid.NewString(), Name: name,
		Input: input, Output: output, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := models.UpsertGuardRailRuleWithConnections(rule, connectionIDs, true); err != nil {
		t.Fatalf("seed guardrail %s: %v", name, err)
	}
	if len(attributes) > 0 {
		if err := models.UpsertGuardrailRuleAttributes(models.DB, uuid.MustParse(testOrgID), name, attributes); err != nil {
			t.Fatalf("attributes of %s: %v", name, err)
		}
	}
	return rule
}

func denyWords(words ...string) map[string]any {
	return map[string]any{"rules": []any{map[string]any{"type": "deny_words_list", "words": words, "pattern_regex": ""}}}
}

var noRules = map[string]any{"rules": []any{}}

func gatewayMasking(t *testing.T, name string, entities []string, connectionIDs []string, attributes []string) *models.DataMaskingRule {
	t.Helper()
	rule := &models.DataMaskingRule{ID: uuid.NewString(), OrgID: testOrgID, Name: name, UpdatedAt: time.Now().UTC(),
		SupportedEntityTypes: models.SupportedEntityTypesList{{Name: "PII", EntityTypes: entities}},
		CustomEntityTypes:    models.CustomEntityTypesList{}, ConnectionIDs: connectionIDs}
	if _, err := models.CreateDataMaskingRule(rule); err != nil {
		t.Fatalf("seed masking %s: %v", name, err)
	}
	if len(attributes) > 0 {
		if err := models.UpsertDatamaskingRuleAttributes(models.DB, uuid.MustParse(testOrgID), name, attributes); err != nil {
			t.Fatalf("attributes of %s: %v", name, err)
		}
	}
	return rule
}

func ruleNames(rules []policy.Rule) string {
	var names []string
	for _, r := range rules {
		names = append(names, r.Name)
	}
	return strings.Join(names, ",")
}

// A gateway guardrail reaches a listener through its mirror by connection id,
// exactly as it reaches an agent connection, and lands after the native rule.
func TestAGatewayGuardrailReachesAMirrorByConnectionID(t *testing.T) {
	startTestDB(t)
	sc, mirror := mirroredSidecar(t, "by-id", mirroredDoc)
	gatewayGuardrail(t, "no-drop", denyWords("DROP TABLE"), noRules, []string{mirror.ID}, nil)
	// A rule on another connection, and a rule on no connection, reach nothing.
	gatewayGuardrail(t, "elsewhere", denyWords("x"), noRules, nil, nil)

	reached, err := models.ListMirrorGuardrailRules(models.DB, uuid.MustParse(testOrgID), sc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(reached) != 1 || reached[0].RuleName != "no-drop" || reached[0].ListenerName != "appdb" {
		t.Fatalf("reached = %+v", reached)
	}
	ids, err := models.SidecarsReachedByGuardrailRule(models.DB, uuid.MustParse(testOrgID), "no-drop")
	if err != nil || len(ids) != 1 || ids[0] != sc.ID {
		t.Fatalf("sidecars reached = %v, %v", ids, err)
	}
	// What the Sidecars page lists, org-wide and for one sidecar.
	for _, scope := range []string{"", sc.ID} {
		bound, err := models.ListMirrorRuleBindings(models.DB, uuid.MustParse(testOrgID), scope)
		if err != nil {
			t.Fatal(err)
		}
		want := []models.SidecarRuleBinding{{SidecarID: sc.ID, Kind: "guardrail", RuleName: "no-drop", ListenerName: "appdb"}}
		if !reflect.DeepEqual(bound, want) {
			t.Fatalf("bindings(%q) = %+v, want %+v", scope, bound, want)
		}
	}

	lane := composedLane(t, sc, "appdb")
	if got := ruleNames(lane.Guardrails.Rules); got != "native,no-drop" {
		t.Fatalf("rules = %s, want the translated rule after the native one", got)
	}
	if r := lane.Guardrails.Rules[1]; r.Type != policy.MatchPattern || r.Pattern != "DROP TABLE" {
		t.Errorf("translation = %+v", r)
	}
}

// A shared attribute binds a gateway rule to a mirror the way it binds the
// rule to an agent connection.
func TestAGatewayRuleReachesAMirrorByAttribute(t *testing.T) {
	startTestDB(t)
	sc, mirror := mirroredSidecar(t, "by-attr", mirroredDoc)
	org := uuid.MustParse(testOrgID)
	if err := models.UpsertConnectionAttributes(models.DB, org, mirror.Name, []string{"prod"}); err != nil {
		t.Fatal(err)
	}
	gatewayGuardrail(t, "prod-guard", denyWords("TRUNCATE"), noRules, nil, []string{"prod"})
	gatewayGuardrail(t, "staging-guard", denyWords("x"), noRules, nil, []string{"staging"})
	gatewayMasking(t, "prod-mask", []string{"EMAIL_ADDRESS"}, nil, []string{"prod"})

	lane := composedLane(t, sc, "appdb")
	if got := ruleNames(lane.Guardrails.Rules); got != "native,prod-guard" {
		t.Fatalf("rules = %s", got)
	}
	if lane.Mask == nil || !strings.Contains(string(lane.Mask.Rules), `"EMAIL_ADDRESS"`) {
		t.Fatalf("mask = %+v", lane.Mask)
	}
	ids, err := models.SidecarsReachedByDataMaskingRule(models.DB, org, "prod-mask")
	if err != nil || len(ids) != 1 || ids[0] != sc.ID {
		t.Fatalf("sidecars reached = %v, %v", ids, err)
	}
	bound, err := models.ListMirrorRuleBindings(models.DB, org, sc.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []models.SidecarRuleBinding{
		{SidecarID: sc.ID, Kind: "datamasking", RuleName: "prod-mask", ListenerName: "appdb"},
		{SidecarID: sc.ID, Kind: "guardrail", RuleName: "prod-guard", ListenerName: "appdb"},
	}
	if !reflect.DeepEqual(bound, want) {
		t.Fatalf("bindings = %+v, want %+v", bound, want)
	}
}

// A masking rule by connection id becomes one mask rule, strategy mask, and
// the write gate composes the sidecar it reaches.
func TestAGatewayMaskingRuleReachesAMirrorByConnectionID(t *testing.T) {
	startTestDB(t)
	sc, mirror := mirroredSidecar(t, "mask-id", mirroredDoc)
	gatewayMasking(t, "pii", []string{"EMAIL_ADDRESS", "US_SSN"}, []string{mirror.ID}, nil)

	lane := composedLane(t, sc, "appdb")
	var rules []struct {
		Name     string   `json:"name"`
		Entities []string `json:"entities"`
		Strategy string   `json:"strategy"`
	}
	if lane.Mask == nil {
		t.Fatal("no mask block")
	}
	if err := json.Unmarshal(lane.Mask.Rules, &rules); err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || rules[0].Name != "pii" || rules[0].Strategy != "mask" || len(rules[0].Entities) != 2 {
		t.Errorf("mask rules = %+v", rules)
	}
	if err := services.CheckSidecarsReachedByGatewayRule(models.DB, testOrgID, services.SidecarRuleMask, "pii"); err != nil {
		t.Errorf("the write gate refused a rule the sidecar runs: %v", err)
	}
}

// The write gate refuses, with the reason, a rule the sidecar cannot run:
// an output rule, a custom entity type. The refusal reads 422.
func TestTheWriteGateRefusesAnUntranslatableRule(t *testing.T) {
	startTestDB(t)
	_, mirror := mirroredSidecar(t, "gate", mirroredDoc)
	withOutput := gatewayGuardrail(t, "with-output", denyWords("x"),
		map[string]any{"rules": []any{map[string]any{"type": "pattern_match", "pattern_regex": "\\d{3}-\\d{2}-\\d{4}"}}},
		[]string{mirror.ID}, nil)
	err := services.CheckSidecarsReachedByGatewayRule(models.DB, testOrgID, services.SidecarRuleGuardrail, "with-output")
	var broken services.ErrSidecarBindingBroken
	if !errors.As(err, &broken) || !strings.Contains(err.Error(), "has output rules") {
		t.Fatalf("want a 422 refusal naming the output rules, got %v", err)
	}
	if err := models.DeleteGuardRailRules(testOrgID, withOutput.ID); err != nil {
		t.Fatal(err)
	}

	custom := &models.DataMaskingRule{ID: uuid.NewString(), OrgID: testOrgID, Name: "custom", UpdatedAt: time.Now().UTC(),
		SupportedEntityTypes: models.SupportedEntityTypesList{{Name: "PII", EntityTypes: []string{"EMAIL_ADDRESS"}}},
		CustomEntityTypes:    models.CustomEntityTypesList{{Name: "ORDER_ID", Regex: "ORD-\\d+", Score: 0.9}},
		ConnectionIDs:        []string{mirror.ID}}
	if _, err := models.CreateDataMaskingRule(custom); err != nil {
		t.Fatal(err)
	}
	err = services.CheckSidecarsReachedByGatewayRule(models.DB, testOrgID, services.SidecarRuleMask, "custom")
	if !errors.As(err, &broken) || !strings.Contains(err.Error(), "custom entity types") {
		t.Fatalf("want a 422 refusal naming the custom entity types, got %v", err)
	}

	// The handshake composes the same document and refuses it the same way.
	sc, err := models.GetSidecarByNameOrID(models.DB, testOrgID, "gate")
	if err != nil {
		t.Fatal(err)
	}
	_, err = services.ComposeSidecarConfiguration(models.DB, sc)
	var refused services.ErrSidecarGatewayRuleRefused
	if !errors.As(err, &refused) {
		t.Fatalf("want the handshake refusal, got %v", err)
	}
}

// The gate refuses only the rule being written. A sidecar another rule
// already breaks lets every other rule through to the handshake.
func TestTheWriteGateRefusesOnlyTheRuleBeingWritten(t *testing.T) {
	startTestDB(t)
	// No native rule: the free tier runs one guardrail per listener.
	_, mirror := mirroredSidecar(t, "crossrule",
		`{"listeners":[{"name":"appdb","protocol":"postgres","listen":":5432","upstream":"db:5432"}]}`)
	org := uuid.MustParse(testOrgID)
	if err := models.UpsertConnectionAttributes(models.DB, org, mirror.Name, []string{"prod"}); err != nil {
		t.Fatal(err)
	}
	gatewayGuardrail(t, "ssn-out", noRules,
		map[string]any{"rules": []any{map[string]any{"type": "pattern_match", "pattern_regex": "\\d{3}-\\d{2}-\\d{4}"}}},
		nil, []string{"prod"})

	gatewayGuardrail(t, "no-drop", denyWords("DROP TABLE"), noRules, []string{mirror.ID}, nil)
	if err := services.CheckSidecarsReachedByGatewayRule(models.DB, testOrgID, services.SidecarRuleGuardrail, "no-drop"); err != nil {
		t.Fatalf("a rule the sidecar runs is refused for another rule's fault: %v", err)
	}
	gatewayMasking(t, "emails", []string{"EMAIL_ADDRESS"}, []string{mirror.ID}, nil)
	if err := services.CheckSidecarsReachedByGatewayRule(models.DB, testOrgID, services.SidecarRuleMask, "emails"); err != nil {
		t.Fatalf("a masking rule the sidecar runs is refused for another rule's fault: %v", err)
	}

	gatewayGuardrail(t, "with-output", denyWords("x"),
		map[string]any{"rules": []any{map[string]any{"type": "pattern_match", "pattern_regex": "secret"}}},
		[]string{mirror.ID}, nil)
	err := services.CheckSidecarsReachedByGatewayRule(models.DB, testOrgID, services.SidecarRuleGuardrail, "with-output")
	var broken services.ErrSidecarBindingBroken
	if !errors.As(err, &broken) || !strings.Contains(err.Error(), `rule "with-output"`) || strings.Contains(err.Error(), "ssn-out") {
		t.Fatalf("want a 422 refusal naming with-output alone, got %v", err)
	}
}

// A sidecar edit that takes an entity out of the detector breaks a gateway
// rule reaching it: the configuration write is refused, like a broken
// native binding.
func TestASidecarEditThatBreaksAReachingRuleIsRefused(t *testing.T) {
	startTestDB(t)
	sc, mirror := mirroredSidecar(t, "pii-edit", mirroredDoc)
	gatewayMasking(t, "ssn", []string{"US_SSN"}, []string{mirror.ID}, nil)
	if err := services.CheckComposedSidecarConfiguration(models.DB, sc); err != nil {
		t.Fatalf("the rule runs on the sidecar as stored: %v", err)
	}
	narrowed := *sc
	narrowed.Configuration.PII = json.RawMessage(`{"entities":["EMAIL_ADDRESS"]}`)
	err := services.CheckComposedSidecarConfiguration(models.DB, &narrowed)
	var broken services.ErrSidecarBindingBroken
	if !errors.As(err, &broken) || !strings.Contains(err.Error(), `"US_SSN"`) {
		t.Fatalf("want a refusal naming the entity, got %v", err)
	}
}

// Removing the listener removes the mirror and, with it, the rule's reach:
// nothing is left to refuse.
func TestAListenerRemovalTakesTheReachWithIt(t *testing.T) {
	startTestDB(t)
	sc, mirror := mirroredSidecar(t, "removed", mirroredDoc)
	gatewayGuardrail(t, "with-output", denyWords("x"),
		map[string]any{"rules": []any{map[string]any{"type": "pattern_match", "pattern_regex": "x"}}},
		[]string{mirror.ID}, nil)
	sc.Configuration.Listeners = []daemon.ListenerConfig{{Name: "other", Protocol: "postgres", Listen: ":5433", Upstream: "db:5432"}}
	if err := services.CheckComposedSidecarConfiguration(models.DB, sc); err != nil {
		t.Fatalf("a rule on a gone listener refused the edit: %v", err)
	}
}
