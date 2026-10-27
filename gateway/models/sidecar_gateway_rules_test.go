package models_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/policy"
)

// storedSidecar is a sidecar with the given configuration stored.
func storedSidecar(t *testing.T, name, doc string) *models.Sidecar {
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
	return stored
}

const bareListenerDoc = `{"listeners":[{"name":"appdb","protocol":"postgres","listen":":5432","upstream":"db:5432"}]}`

const nativeRuleDoc = `{"listeners":[{"name":"appdb","protocol":"postgres","listen":":5432","upstream":"db:5432",
	"guardrails":{"rules":[{"name":"native","type":"operation","operations":["truncate"]}]}}]}`

func appdbOn(sc *models.Sidecar) []models.SidecarRuleTarget {
	return []models.SidecarRuleTarget{{SidecarID: sc.ID, ListenerName: "appdb"}}
}

// agentGuardrail is a guardrail rule as the Guardrails page writes it:
// input/output rules, no sidecar_spec, bound to listeners through the junction.
func agentGuardrail(t *testing.T, name string, input, output map[string]any, sc *models.Sidecar) *models.GuardRailRules {
	t.Helper()
	rule := &models.GuardRailRules{OrgID: testOrgID, ID: uuid.NewString(), Name: name,
		Input: input, Output: output, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := models.UpsertGuardRailRuleWithConnections(rule, nil, true); err != nil {
		t.Fatalf("seed guardrail %s: %v", name, err)
	}
	if sc != nil {
		if err := models.SetGuardrailRuleListeners(models.DB, uuid.MustParse(testOrgID), name, appdbOn(sc)); err != nil {
			t.Fatalf("bind %s: %v", name, err)
		}
	}
	return rule
}

func denyWords(words ...string) map[string]any {
	return map[string]any{"rules": []any{map[string]any{"type": "deny_words_list", "words": words, "pattern_regex": ""}}}
}

var noRules = map[string]any{"rules": []any{}}

// agentMasking is a data masking rule as the Data Masking page writes it.
func agentMasking(t *testing.T, name string, entities []string, sc *models.Sidecar) *models.DataMaskingRule {
	t.Helper()
	rule := &models.DataMaskingRule{ID: uuid.NewString(), OrgID: testOrgID, Name: name, UpdatedAt: time.Now().UTC(),
		SupportedEntityTypes: models.SupportedEntityTypesList{{Name: "PII", EntityTypes: entities}},
		CustomEntityTypes:    models.CustomEntityTypesList{}}
	if _, err := models.CreateDataMaskingRule(rule); err != nil {
		t.Fatalf("seed masking %s: %v", name, err)
	}
	if sc != nil {
		if err := models.SetDataMaskingRuleListeners(models.DB, uuid.MustParse(testOrgID), name, appdbOn(sc)); err != nil {
			t.Fatalf("bind %s: %v", name, err)
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

// The junction read carries the agent fields of a rule with no sidecar_spec,
// beside the spec of a native one, and the binding list tells them apart.
func TestTheJunctionReadCarriesTheAgentFields(t *testing.T) {
	startTestDB(t)
	sc := storedSidecar(t, "junction", bareListenerDoc)
	agentGuardrail(t, "no-drop", denyWords("DROP TABLE"), noRules, sc)
	native := &models.GuardRailRules{OrgID: testOrgID, ID: uuid.NewString(), Name: "native-only",
		SidecarSpec: json.RawMessage(`{"rules":[{"name":"n","type":"operation","operations":["drop"]}]}`),
		CreatedAt:   time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := models.UpsertGuardRailRuleWithConnections(native, nil, true); err != nil {
		t.Fatal(err)
	}
	if err := models.SetGuardrailRuleListeners(models.DB, uuid.MustParse(testOrgID), native.Name, appdbOn(sc)); err != nil {
		t.Fatal(err)
	}
	low := 0.5
	mask := &models.DataMaskingRule{ID: uuid.NewString(), OrgID: testOrgID, Name: "emails", UpdatedAt: time.Now().UTC(),
		SupportedEntityTypes: models.SupportedEntityTypesList{{Name: "PII", EntityTypes: []string{"EMAIL_ADDRESS"}}},
		CustomEntityTypes:    models.CustomEntityTypesList{{Name: "ORDER_ID", Regex: `ORD-\d+`, Score: 0.9}},
		ScoreThreshold:       &low}
	if _, err := models.CreateDataMaskingRule(mask); err != nil {
		t.Fatal(err)
	}
	if err := models.SetDataMaskingRuleListeners(models.DB, uuid.MustParse(testOrgID), mask.Name, appdbOn(sc)); err != nil {
		t.Fatal(err)
	}

	guardrails, err := models.ListGuardrailRulesForSidecar(models.DB, uuid.MustParse(testOrgID), sc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(guardrails) != 2 {
		t.Fatalf("want two bound guardrails, got %+v", guardrails)
	}
	byName := map[string]models.BoundRule{}
	for _, b := range guardrails {
		byName[b.RuleName] = b
	}
	agent := byName["no-drop"]
	var output struct {
		Rules []any `json:"rules"`
	}
	if err := json.Unmarshal(agent.Output, &output); err != nil {
		t.Fatalf("output read = %s: %v", agent.Output, err)
	}
	if agent.HasSpec() || agent.ListenerName != "appdb" || !strings.Contains(string(agent.Input), "DROP TABLE") || len(output.Rules) != 0 {
		t.Errorf("agent rule read = %+v", agent)
	}
	if n := byName["native-only"]; !n.HasSpec() || len(n.Input) > 0 && string(n.Input) != "null" {
		t.Errorf("native rule read = %+v", n)
	}

	masking, err := models.ListDataMaskingRulesForSidecar(models.DB, uuid.MustParse(testOrgID), sc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(masking) != 1 || masking[0].HasSpec() || len(masking[0].SupportedEntityTypes) != 1 ||
		masking[0].SupportedEntityTypes[0].EntityTypes[0] != "EMAIL_ADDRESS" ||
		len(masking[0].CustomEntityTypes) != 1 || masking[0].CustomEntityTypes[0].Name != "ORDER_ID" ||
		masking[0].ScoreThreshold == nil || *masking[0].ScoreThreshold != 0.5 {
		t.Errorf("masking rule read = %+v", masking)
	}

	bound, err := models.ListSidecarRuleBindings(models.DB, uuid.MustParse(testOrgID), sc.ID)
	if err != nil {
		t.Fatal(err)
	}
	translated := map[string]bool{}
	for _, b := range bound {
		translated[b.Kind+"/"+b.RuleName] = b.Translated
	}
	if len(bound) != 3 || !translated["guardrail/no-drop"] || translated["guardrail/native-only"] || !translated["datamasking/emails"] {
		t.Errorf("bindings = %+v", bound)
	}
}

// Composition translates a bound agent rule after the listener's native
// rules, and a masking rule into the lane's mask block.
func TestCompositionTranslatesABoundAgentRule(t *testing.T) {
	startTestDB(t)
	sc := storedSidecar(t, "compose", nativeRuleDoc)
	agentGuardrail(t, "no-drop", denyWords("DROP TABLE"), noRules, sc)
	agentMasking(t, "emails", []string{"EMAIL_ADDRESS"}, sc)
	// Rules bound nowhere reach nothing.
	agentGuardrail(t, "elsewhere", denyWords("x"), noRules, nil)
	agentMasking(t, "unbound", []string{"US_SSN"}, nil)

	composed, err := services.ComposeSidecarConfiguration(models.DB, sc)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	lane := composed.Listeners[0]
	if lane.Guardrails == nil || ruleNames(lane.Guardrails.Rules) != "native,no-drop" {
		t.Fatalf("guardrails = %+v", lane.Guardrails)
	}
	if got := lane.Guardrails.Rules[1]; got.Type != policy.MatchPattern || got.Pattern != "DROP TABLE" {
		t.Errorf("translation = %+v", got)
	}
	if lane.Mask == nil || !strings.Contains(string(lane.Mask.Rules), `"EMAIL_ADDRESS"`) ||
		strings.Contains(string(lane.Mask.Rules), "US_SSN") {
		t.Errorf("mask = %+v", lane.Mask)
	}
	if stored, _ := models.GetSidecarByNameOrID(models.DB, testOrgID, sc.ID); len(stored.Configuration.Listeners[0].Guardrails.Rules) != 1 {
		t.Error("composition wrote the translation into the stored configuration")
	}
}

// The write gate translates an agent rule for each target and refuses what
// the sidecar cannot run, with the reason.
func TestTheWriteGateRefusesAnUntranslatableAgentRule(t *testing.T) {
	startTestDB(t)
	sc := storedSidecar(t, "gate", bareListenerDoc)
	targets := appdbOn(sc)
	gate := func(kind services.SidecarRuleKind, rule models.BoundRule) error {
		return services.ValidateSidecarRuleTargets(models.DB, testOrgID, kind, "", rule, targets)
	}
	guardrail := func(name, input, output string) models.BoundRule {
		return models.BoundRule{RuleName: name, AgentRule: models.AgentRule{
			Input: json.RawMessage(input), Output: json.RawMessage(output)}}
	}
	masking := func(name string, entities []string, threshold *float64, custom ...models.CustomEntityTypesEntry) models.BoundRule {
		return models.BoundRule{RuleName: name, AgentRule: models.AgentRule{
			SupportedEntityTypes: models.SupportedEntityTypesList{{Name: "PII", EntityTypes: entities}},
			CustomEntityTypes:    custom, ScoreThreshold: threshold}}
	}

	if err := gate(services.SidecarRuleGuardrail, guardrail("no-drop",
		`{"rules":[{"type":"deny_words_list","words":["DROP TABLE"]}]}`, `{"rules":[]}`)); err != nil {
		t.Errorf("a rule the sidecar runs was refused: %v", err)
	}
	err := gate(services.SidecarRuleGuardrail, guardrail("with-output",
		`{"rules":[{"type":"deny_words_list","words":["x"]}]}`, `{"rules":[{"type":"pattern_match","pattern_regex":"secret"}]}`))
	if err == nil || !strings.Contains(err.Error(), `guardrail rule "with-output", bound to listener "appdb", has output rules`) {
		t.Errorf("want the output rules refused, got %v", err)
	}
	err = gate(services.SidecarRuleGuardrail, guardrail("empty", `{"rules":[{"type":"deny_words_list","words":[""]}]}`, ``))
	if err == nil || !strings.Contains(err.Error(), "has no rules") {
		t.Errorf("want a rule that translates to nothing refused, got %v", err)
	}

	if err := gate(services.SidecarRuleMask, masking("emails", []string{"EMAIL_ADDRESS"}, nil)); err != nil {
		t.Errorf("a masking rule the sidecar runs was refused: %v", err)
	}
	err = gate(services.SidecarRuleMask, masking("ids", []string{"EMAIL_ADDRESS"}, nil,
		models.CustomEntityTypesEntry{Name: "ORDER_ID", Regex: `ORD-\d+`, Score: 0.9}))
	if err == nil || !strings.Contains(err.Error(), "has custom entity types") {
		t.Errorf("want the custom entity refused, got %v", err)
	}
	low := 0.2
	err = gate(services.SidecarRuleMask, masking("low", []string{"EMAIL_ADDRESS"}, &low))
	if err == nil || !strings.Contains(err.Error(), "score threshold of 0.2") {
		t.Errorf("want the threshold refused, got %v", err)
	}

	// The entity check reads THAT sidecar's pii section.
	narrow := storedSidecar(t, "narrow", `{"pii":{"entities":["US_SSN"]},`+bareListenerDoc[1:])
	err = services.ValidateSidecarRuleTargets(models.DB, testOrgID, services.SidecarRuleMask, "",
		masking("emails", []string{"EMAIL_ADDRESS"}, nil), appdbOn(narrow))
	if err == nil || !strings.Contains(err.Error(), `names entity "EMAIL_ADDRESS"`) {
		t.Errorf("want the unloaded entity refused, got %v", err)
	}
}

// A configuration edit that takes an entity away from a bound masking rule
// is refused, and the rule's own binding list is what the edit is checked
// against.
func TestAPiiEditUnderABoundAgentRuleIsRefused(t *testing.T) {
	startTestDB(t)
	sc := storedSidecar(t, "pii-edit", bareListenerDoc)
	agentMasking(t, "ssn", []string{"US_SSN"}, sc)
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

	// Removing the bound listener is refused the way it is for a native rule.
	gone := *sc
	gone.Configuration.Listeners = []daemon.ListenerConfig{{Name: "other", Protocol: "postgres", Listen: ":5433", Upstream: "db:5432"}}
	if err := services.ValidateSidecarBindingsForConfiguration(models.DB, &gone); err == nil {
		t.Fatal("dropping a listener with a bound agent rule must be refused")
	}
}
