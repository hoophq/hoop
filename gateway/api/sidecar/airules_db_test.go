package apisidecar

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	apiai "github.com/hoophq/hoop/gateway/api/ai"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/license/licensetest"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ENG-662: an analyzer block's ai_rules.<level> lets the AI apply library rules.

// seedGuardrailLibraryRule stores a guardrail rule with a sidecar block,
// bound nowhere.
func seedGuardrailLibraryRule(t *testing.T, name, spec string) {
	t.Helper()
	require.NoError(t, models.UpsertGuardRailRuleWithConnections(&models.GuardRailRules{
		OrgID: statusTestOrgID, ID: uuid.NewString(), Name: name,
		Input: map[string]any{"rules": []any{}}, Output: map[string]any{"rules": []any{}},
		SidecarSpec: json.RawMessage(spec),
	}, nil, true))
}

func seedMaskLibraryRule(t *testing.T, name, spec string) {
	t.Helper()
	_, err := models.CreateDataMaskingRule(&models.DataMaskingRule{
		ID: uuid.NewString(), OrgID: statusTestOrgID, Name: name, SidecarSpec: json.RawMessage(spec),
	})
	require.NoError(t, err)
}

// setOrgLicense stores the organization's license document; empty removes it.
func setOrgLicense(t *testing.T, doc string) {
	t.Helper()
	var value any
	if doc != "" {
		value = doc
	}
	require.NoError(t, models.DB.Exec(`UPDATE private.orgs SET license_data = ?::json WHERE id = ?`,
		value, statusTestOrgID).Error)
}

// enterpriseLicenseWith is licenseDoc granting only features.
func enterpriseLicenseWith(features ...string) string {
	list, _ := json.Marshal(features)
	return strings.Replace(licenseDoc, `"description":"Acme"`, `"description":"Acme","features":`+string(list), 1)
}

func callAnalyzerRule(t *testing.T, handler gin.HandlerFunc, method, name, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set(storagev2.ContextKey, storagev2.NewContext("admin-1", statusTestOrgID).
		WithUserInfo("Admin", "admin@hoop.dev", "active", "", []string{"admin"}))
	c.Params = gin.Params{{Key: "name", Value: name}}
	c.Request = httptest.NewRequest(method, "/api/ai/session-analyzer/rules/"+name, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	handler(c)
	return rec
}

func analyzerRuleBody(name, spec, targets string) string {
	body := `{"name":"` + name + `","connection_names":[],"agentic":false,` +
		`"risk_evaluation":{"low_risk_action":"allow_execution","medium_risk_action":"allow_execution",` +
		`"high_risk_action":"allow_execution"},"sidecar_spec":` + spec
	if targets != "" {
		body += `,"sidecar_targets":` + targets
	}
	return body + `}`
}

// aiRulesSidecar stores a sidecar with the analyzer section, so an analyzer
// rule can bind to its appdb listener, and a build that reads AI rules.
func aiRulesSidecar(t *testing.T, name string, caps ...string) *models.Sidecar {
	t.Helper()
	sc := &models.Sidecar{
		OrgID: statusTestOrgID, Name: name, KeyHash: models.HashAPIKey("hsc_" + name), CreatedBy: "tests@hoop.dev",
		Configuration: models.SidecarConfiguration{
			Analyzer: &daemon.AnalyzerConfig{Provider: "anthropic", Model: "m"},
			Listeners: []daemon.ListenerConfig{{
				Name: "appdb", Protocol: "postgres", Listen: ":5432", Upstream: "db:5432",
			}},
		},
	}
	require.NoError(t, models.CreateSidecar(models.DB, sc))
	if caps == nil {
		caps = daemon.SidecarCapabilities()
	}
	_, err := models.RecordSidecarHandshake(models.DB, sc.ID, "1.300.0", "", "", "", "", caps)
	require.NoError(t, err)
	stored, err := models.GetSidecarByNameOrID(models.DB, statusTestOrgID, sc.ID)
	require.NoError(t, err)
	return stored
}

const aiRulesSpec = `{"high":"block","medium":"warn","low":"allow","ai_rules":{` +
	`"medium":{"guardrails":{"rules":["no-drop"]}},"low":{"mask":{"mode":"existing_and_new"}}}}`

func TestAnAnalyzerRuleSavesAIRulesOnlyWhenTheSidecarCanRunThem(t *testing.T) {
	startStatusTestDB(t)
	seedGuardrailLibraryRule(t, "no-drop", `{"rules":[{"name":"no-drop","type":"operation","operations":["drop"]}]}`)
	seedGuardrailLibraryRule(t, "gateway-only", "null")
	seedMaskLibraryRule(t, "emails", `{"rules":[{"name":"emails","entities":["EMAIL_ADDRESS"]}]}`)
	sc := aiRulesSidecar(t, "ai-rules-save")
	targets := fmt.Sprintf(`[{"sidecar_id":%q,"listener_name":"appdb"}]`, sc.ID)

	// No license: refused, bound or not.
	for _, tg := range []string{"", targets} {
		rec := callAnalyzerRule(t, apiai.CreateSessionAnalyzerRule, http.MethodPost, "",
			analyzerRuleBody("ai-rules", aiRulesSpec, tg))
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), "Enterprise license")
	}
	// An Enterprise license without data masking: the mask options are refused.
	setOrgLicense(t, enterpriseLicenseWith("guardrails"))
	rec := callAnalyzerRule(t, apiai.CreateSessionAnalyzerRule, http.MethodPost, "",
		analyzerRuleBody("ai-rules", aiRulesSpec, targets))
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "data-masking")

	setOrgLicense(t, licenseDoc)
	for name, c := range map[string]struct{ spec, want string }{
		"an unknown name": {`{"medium":"warn","ai_rules":{"medium":{"guardrails":{"rules":["no-such-rule"]}}}}`,
			`names \"no-such-rule\"`},
		"a rule with no sidecar block": {`{"medium":"warn","ai_rules":{"medium":{"guardrails":{"rules":["gateway-only"]}}}}`,
			`names \"gateway-only\"`},
		"a mask name of the guardrail library": {`{"medium":"warn","ai_rules":{"medium":{"mask":{"rules":["no-drop"]}}}}`,
			`names \"no-drop\"`},
		"a block level":   {`{"high":"block","ai_rules":{"high":{"guardrails":{}}}}`, `ai_rules.high`},
		"a defer level":   {`{"high":"defer","ai_rules":{"high":{"mask":{}}}}`, `ai_rules.high`},
		"an empty entry":  {`{"medium":"warn","ai_rules":{"medium":{}}}`, "neither guardrails nor mask"},
		"an unknown mode": {`{"medium":"warn","ai_rules":{"medium":{"mask":{"mode":"sometimes"}}}}`, `mode \"sometimes\"`},
	} {
		rec := callAnalyzerRule(t, apiai.CreateSessionAnalyzerRule, http.MethodPost, "",
			analyzerRuleBody("ai-rules-refused", c.spec, targets))
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "%s: %s", name, rec.Body.String())
		assert.Contains(t, rec.Body.String(), c.want, name)
	}

	rec = callAnalyzerRule(t, apiai.CreateSessionAnalyzerRule, http.MethodPost, "",
		analyzerRuleBody("ai-rules", aiRulesSpec, targets))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	// The library is served to the bound sidecar, the level naming it.
	served, err := services.ComposeSidecarConfiguration(models.DB, sc)
	require.NoError(t, err)
	require.NotNil(t, served.RuleLibrary)
	require.Len(t, served.RuleLibrary.Guardrails, 1)
	assert.Equal(t, "no-drop", served.RuleLibrary.Guardrails[0].Name)
	assert.JSONEq(t, `[{"name":"emails","entities":["EMAIL_ADDRESS"]}]`, string(served.RuleLibrary.Mask))

	// An edit that says nothing about the sidecar block is checked against
	// the stored one: the license lapsed, so it is refused.
	setOrgLicense(t, "")
	rec = callAnalyzerRule(t, apiai.UpdateSessionAnalyzerRule, http.MethodPut, "ai-rules",
		`{"name":"ai-rules","connection_names":[],"agentic":false,"description":"edited",`+
			`"risk_evaluation":{"low_risk_action":"allow_execution","medium_risk_action":"allow_execution",`+
			`"high_risk_action":"allow_execution"}}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}

// A configuration write checks its listener ai_rules as an analyzer rule save
// does: the sidecar's refusals, and no name that neither the library nor the
// stored rule_library holds.
func TestAConfigurationWriteChecksItsListenerAIRules(t *testing.T) {
	startStatusTestDB(t)
	// Signed, so the write's license cap lets AI rules through.
	setOrgLicense(t, licensetest.Document(t, licensetest.Enterprise()))
	seedGuardrailLibraryRule(t, "no-drop", `{"rules":[{"name":"no-drop","type":"operation","operations":["drop"]}]}`)
	sc := aiRulesSidecar(t, "ai-rules-write")
	doc := func(block, library string) string {
		return `{"analyzer":{"provider":"anthropic","model":"m"},` + library + `"listeners":[{"name":"appdb",` +
			`"protocol":"postgres","listen":":5432","upstream":"db:5432","analyzer":` + block + `}]}`
	}
	writes := []struct {
		method  string
		handler gin.HandlerFunc
	}{{http.MethodPut, Put}, {http.MethodPatch, Patch}}

	for name, c := range map[string]struct {
		block string
		want  []string
	}{
		"an unknown name": {`{"medium":"warn","ai_rules":{"medium":{"guardrails":{"rules":["no-such-rule"]}}}}`,
			[]string{`listener \"appdb\" analyzer block`, `names \"no-such-rule\"`}},
		"a mask name of the guardrail library": {`{"medium":"warn","ai_rules":{"medium":{"mask":{"rules":["no-drop"]}}}}`,
			[]string{`listener \"appdb\" analyzer block`, `names \"no-drop\"`}},
		"a block level":  {`{"medium":"block","ai_rules":{"medium":{"guardrails":{}}}}`, []string{`ai_rules.medium`}},
		"an empty entry": {`{"medium":"warn","ai_rules":{"medium":{}}}`, []string{"neither guardrails nor mask"}},
	} {
		for _, wr := range writes {
			w, _ := callAdminIn(t, statusTestOrgID, wr.handler, wr.method, sc.ID, doc(c.block, ""))
			assert.Equal(t, http.StatusUnprocessableEntity, w.Code, "%s %s: %s", wr.method, name, w.Body)
			for _, want := range c.want {
				assert.Contains(t, w.Body.String(), want, "%s %s", wr.method, name)
			}
		}
	}

	for name, c := range map[string]struct{ block, library string }{
		"a library rule": {`{"medium":"warn","ai_rules":{"medium":{"guardrails":{"rules":["no-drop"]}}}}`, ""},
		"a rule of the stored rule_library": {`{"medium":"warn","ai_rules":{"medium":{"guardrails":{"rules":["own"]}}}}`,
			`"rule_library":{"guardrails":[{"name":"own","type":"deny_words_list","words":["x"]}]},`},
	} {
		for _, wr := range writes {
			w, _ := callAdminIn(t, statusTestOrgID, wr.handler, wr.method, sc.ID, doc(c.block, c.library))
			assert.Equal(t, http.StatusOK, w.Code, "%s %s: %s", wr.method, name, w.Body)
		}
	}
}

func TestTheLibraryRulesTheLevelsSelectAreServed(t *testing.T) {
	startStatusTestDB(t)
	seedGuardrailLibraryRule(t, "no-drop", `{"rules":[{"name":"no-drop","type":"operation","operations":["drop"]}]}`)
	seedGuardrailLibraryRule(t, "writes", `{"rules":[{"name":"no-delete","type":"operation","operations":["delete"]},`+
		`{"name":"no-update","type":"operation","operations":["update"]}]}`)
	seedGuardrailLibraryRule(t, "unselected", `{"rules":[{"name":"u","type":"operation","operations":["insert"]}]}`)
	seedMaskLibraryRule(t, "emails", `{"rules":[{"name":"emails","entities":["EMAIL_ADDRESS"]}]}`)
	sc := seedLevelSidecar(t, "ai-rules-compose", "ai-rules-compose",
		`{"medium":"warn","ai_rules":{"medium":{"guardrails":{"rules":["writes","no-drop"]}}}}`)

	served, err := services.ComposeSidecarConfiguration(models.DB, sc)
	require.NoError(t, err)
	require.NotNil(t, served.RuleLibrary)
	var names []string
	for _, r := range served.RuleLibrary.Guardrails {
		names = append(names, r.Name)
	}
	assert.Equal(t, []string{"no-drop", "writes#1", "writes#2"}, names, "the selected rules only")
	assert.Empty(t, served.RuleLibrary.Mask, "no level uses mask")
	assert.Equal(t, []string{"writes#1", "writes#2", "no-drop"},
		served.Listeners[0].Analyzer.LevelAIRule(analyzer.RiskMedium).Guardrails.Rules)

	// rules absent: every library rule of that kind.
	setLevelSpec(t, "ai-rules-compose", `{"medium":"warn","low":"allow","ai_rules":{"medium":{"guardrails":{}},"low":{"mask":{}}}}`)
	served, err = services.ComposeSidecarConfiguration(models.DB, sc)
	require.NoError(t, err)
	assert.Len(t, served.RuleLibrary.Guardrails, 4)
	assert.JSONEq(t, `[{"name":"emails","entities":["EMAIL_ADDRESS"]}]`, string(served.RuleLibrary.Mask))

	// A library edit reaches the next composition, as a rule edit does.
	require.NoError(t, models.DB.Exec(`UPDATE private.datamasking_rules SET sidecar_spec = ?::jsonb
		WHERE org_id = ? AND name = 'emails'`, `{"rules":[{"name":"emails","entities":["EMAIL_ADDRESS"],"strategy":"hash"}]}`,
		statusTestOrgID).Error)
	served, err = services.ComposeSidecarConfiguration(models.DB, sc)
	require.NoError(t, err)
	assert.JSONEq(t, `[{"name":"emails","entities":["EMAIL_ADDRESS"],"strategy":"hash"}]`, string(served.RuleLibrary.Mask))

	// The stored configuration is untouched.
	stored, err := models.GetSidecarByNameOrID(models.DB, statusTestOrgID, sc.ID)
	require.NoError(t, err)
	assert.Nil(t, stored.Configuration.RuleLibrary)
	assert.Nil(t, stored.Configuration.Listeners[0].Analyzer)

	// No level with AI rules: nothing is served.
	setLevelSpec(t, "ai-rules-compose", `{"high":"block","medium":"warn"}`)
	served, err = services.ComposeSidecarConfiguration(models.DB, sc)
	require.NoError(t, err)
	assert.Nil(t, served.RuleLibrary)

	// Names never collide: a row named like another row's rule, and a
	// stored rule_library rule named like a library rule. Delivery goes on.
	seedGuardrailLibraryRule(t, "writes#1", `{"rules":[{"name":"w","type":"operation","operations":["insert"]}]}`)
	setLevelSpec(t, "ai-rules-compose", `{"medium":"warn","ai_rules":{"medium":{"guardrails":{}}}}`)
	row, err := models.GetSidecarByNameOrID(models.DB, statusTestOrgID, sc.ID)
	require.NoError(t, err)
	row.Configuration.RuleLibrary = &daemon.RuleLibrary{Guardrails: []policy.Rule{
		{Name: "no-drop", Type: "operation", Operations: []inspect.Operation{"drop"}}}}
	_, err = models.UpdateSidecarConfiguration(models.DB, statusTestOrgID, sc.ID, row.Configuration)
	require.NoError(t, err)
	row, err = models.GetSidecarByNameOrID(models.DB, statusTestOrgID, sc.ID)
	require.NoError(t, err)
	served, err = services.ComposeSidecarConfiguration(models.DB, row)
	require.NoError(t, err)
	names = nil
	for _, r := range served.RuleLibrary.Guardrails {
		names = append(names, r.Name)
	}
	assert.ElementsMatch(t, []string{"no-drop", "no-drop#dup1", "unselected", "writes#1", "writes#2", "writes##1"}, names)
}

// An import keeps rule_library in the sidecar's document and moves ai_rules
// into an analyzer rule. A save of that rule resolves its names against the
// rule_library of each sidecar it binds to, as composition serves them.
func TestAnImportedAnalyzerRuleKeepsItsRuleLibrarySelection(t *testing.T) {
	startStatusTestDB(t)
	setOrgLicense(t, licensetest.Document(t, licensetest.Enterprise()))
	sc := &models.Sidecar{OrgID: statusTestOrgID, Name: "ai-rules-import",
		KeyHash: models.HashAPIKey("hsc_ai-rules-import"), CreatedBy: "tests@hoop.dev"}
	require.NoError(t, models.CreateSidecar(models.DB, sc))
	_, err := models.RecordSidecarHandshake(models.DB, sc.ID, "1.300.0", "", "", "", "", daemon.SidecarCapabilities())
	require.NoError(t, err)
	const spec = `{"medium":"warn","ai_rules":{"medium":{"guardrails":{"rules":["local-only"]}}}}`
	file := `{"analyzer":{"provider":"anthropic","model":"m"},` +
		`"rule_library":{"guardrails":[{"name":"local-only","type":"deny_words_list","words":["x"]}]},` +
		`"listeners":[{"name":"appdb","protocol":"postgres","listen":":5432","upstream":"db:5432","analyzer":` +
		spec + `}]}`
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/sidecars/configuration", strings.NewReader(file))
	c.Set("sidecar-auth", sc)
	ImportConfiguration(c)
	require.Equal(t, http.StatusOK, w.Code, "import: %s", w.Body)

	// An edit that leaves the imported selection as it is.
	const rule = "ai-rules-import-appdb-analyzer"
	rec := callAnalyzerRule(t, apiai.UpdateSessionAnalyzerRule, http.MethodPut, rule,
		`{"name":"`+rule+`","connection_names":[],"agentic":false,"description":"edited",`+
			`"risk_evaluation":{"low_risk_action":"allow_execution","medium_risk_action":"allow_execution",`+
			`"high_risk_action":"allow_execution"}}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// A sidecar whose rule_library does not hold the name cannot run it.
	other := aiRulesSidecar(t, "ai-rules-other")
	for name, targets := range map[string]string{
		"the other sidecar": fmt.Sprintf(`[{"sidecar_id":%q,"listener_name":"appdb"}]`, other.ID),
		"both sidecars": fmt.Sprintf(`[{"sidecar_id":%q,"listener_name":"appdb"},{"sidecar_id":%q,"listener_name":"appdb"}]`,
			sc.ID, other.ID),
	} {
		rec = callAnalyzerRule(t, apiai.UpdateSessionAnalyzerRule, http.MethodPut, rule,
			analyzerRuleBody(rule, spec, targets))
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "%s: %s", name, rec.Body.String())
		assert.Contains(t, rec.Body.String(), `names \"local-only\"`, name)
		assert.Contains(t, rec.Body.String(), `ai-rules-other`, name)
	}
	rec = callAnalyzerRule(t, apiai.UpdateSessionAnalyzerRule, http.MethodPut, rule,
		analyzerRuleBody(rule, spec, fmt.Sprintf(`[{"sidecar_id":%q,"listener_name":"appdb"}]`, sc.ID)))
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// No build before ENG-662 can apply an AI rule, so the plane refuses to serve
// one, at the handshake and when a rule is saved.
func TestAnOldBuildIsRefusedAIRules(t *testing.T) {
	startStatusTestDB(t)
	seedGuardrailLibraryRule(t, "no-drop", `{"rules":[{"name":"no-drop","type":"operation","operations":["drop"]}]}`)
	sc := seedLevelSidecar(t, "ai-rules-old", "ai-rules-old",
		`{"medium":"warn","ai_rules":{"medium":{"guardrails":{"rules":["no-drop"]}}}}`)
	setOrgLicense(t, licenseDoc)

	for _, caps := range []string{"", daemon.CapabilityLevelApprovers,
		daemon.CapabilityLevelApprovers + "," + daemon.CapabilityAIHandoff} {
		w := handshake(t, sc, caps)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code, "caps %q: %s", caps, w.Body.String())
		assert.Contains(t, w.Body.String(), daemon.CapabilityAIRules, "caps %q", caps)
	}
	// A build that reads AI rules passes the capability check. The license
	// the sidecar verifies is checked after it; this test document carries
	// no signature the sidecar trusts, so it is refused there instead.
	w := handshake(t, sc, daemon.CapabilityLevelApprovers+","+daemon.CapabilityAIRules)
	assert.NotEqual(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
	assert.NotContains(t, w.Body.String(), daemon.CapabilityAIRules, w.Body.String())

	// Save time: the bound sidecar last handshook without the capability.
	row, err := models.GetSidecarByNameOrID(models.DB, statusTestOrgID, sc.ID)
	require.NoError(t, err)
	_, err = models.RecordSidecarHandshake(models.DB, sc.ID, "1.191.0", "", "", "", "",
		[]string{daemon.CapabilityLevelApprovers})
	require.NoError(t, err)
	row.Configuration.Analyzer = &daemon.AnalyzerConfig{Provider: "anthropic", Model: "m"}
	_, err = models.UpdateSidecarConfiguration(models.DB, statusTestOrgID, sc.ID, row.Configuration)
	require.NoError(t, err)
	spec := json.RawMessage(`{"medium":"warn","ai_rules":{"medium":{"guardrails":{"rules":["no-drop"]}}}}`)
	targets := []models.SidecarRuleTarget{{SidecarID: sc.ID, ListenerName: "appdb"}}
	err = services.ValidateSidecarRuleTargets(models.DB, statusTestOrgID, services.SidecarRuleAnalyzer,
		"ai-rules-old", "ai-rules-old", spec, targets)
	require.Error(t, err)
	assert.Contains(t, err.Error(), daemon.CapabilityAIRules)

	_, err = models.RecordSidecarHandshake(models.DB, sc.ID, "1.300.0", "", "", "", "",
		[]string{daemon.CapabilityLevelApprovers, daemon.CapabilityAIRules})
	require.NoError(t, err)
	require.NoError(t, services.ValidateSidecarRuleTargets(models.DB, statusTestOrgID, services.SidecarRuleAnalyzer,
		"ai-rules-old", "ai-rules-old", spec, targets))
}

// The AI rules a sidecar reports are stored in the session's ai_decisions
// entry, bounded, and the entries stay under the byte ceiling as stored.
func TestPostEventsStoresTheAIRules(t *testing.T) {
	startEventsDB(t, eventsAIDecisionsOrgID)
	enableSessionEvents(t, eventsAIDecisionsOrgID)
	sc := seedEventsSidecar(t, eventsAIDecisionsOrgID, "ai-rules-events")

	reason := strings.Repeat("é", 2000)
	var rules []map[string]any
	for i := range 25 {
		rules = append(rules, map[string]any{"kind": "mask", "reason": reason,
			"definition": map[string]any{"columns": []string{"a", "b", "c"}, "keep_last": i, "nested": map[string]any{"x": []int{1, 2}}}})
	}
	rules[0] = map[string]any{"kind": "guardrail", "rule": "no-drop", "reason": "drops\x00", "matched": true}
	raw, err := json.Marshal(rules)
	require.NoError(t, err)

	seq := int64(1)
	events := []daemon.SessionEvent{sessionEvent(seq, "s-rules", 0, audit.KindSessionStart, nil)}
	for range 20 {
		seq++
		events = append(events, sessionEvent(seq, "s-rules", int(seq), audit.KindStatement, func(e *audit.Event) {
			e.Statement = "SELECT 1"
			e.Metadata = map[string]string{analyzer.MetadataRiskLevel: "low", analyzer.MetadataAction: "allow",
				analyzer.MetadataAIRules: string(raw), analyzer.MetadataAIRulesError: "the rule agent timed out"}
		}))
	}
	rec := postEvents(sc, eventsBody(t, events...))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	id := services.SidecarSessionID(sc.ID, "s-rules")
	var stored struct {
		Bytes   int
		Count   int
		Dropped int
	}
	require.NoError(t, models.DB.Raw(`SELECT octet_length((metadata->'sidecar'->'ai_decisions')::text) AS bytes,
		jsonb_array_length(metadata->'sidecar'->'ai_decisions') AS count,
		COALESCE((metadata->'sidecar'->>'ai_decisions_dropped')::int, 0) AS dropped
		FROM private.sessions WHERE org_id = ? AND id = ?`, eventsAIDecisionsOrgID, id).Scan(&stored).Error)
	assert.LessOrEqual(t, stored.Bytes, 256<<10, "the list stays under its byte ceiling")
	assert.Positive(t, stored.Count)
	assert.Positive(t, stored.Dropped, "the entries past the ceiling are counted")
	assert.Equal(t, 20, stored.Count+stored.Dropped)

	list, ok := sidecarMetadata(t, getSidecarSessionRow(t, eventsAIDecisionsOrgID, id))["ai_decisions"].([]any)
	require.True(t, ok)
	entry := list[0].(map[string]any)
	applied, ok := entry["ai_rules"].([]any)
	require.True(t, ok)
	assert.Len(t, applied, 20, "at most 20 rules per entry")
	assert.Equal(t, map[string]any{"kind": "guardrail", "rule": "no-drop", "reason": "drops", "matched": true}, applied[0])
	assert.Equal(t, "the rule agent timed out", entry["ai_rules_error"])
}
