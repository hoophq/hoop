// Package gatewayrulestest drives the rule APIs and the sidecar handshake
// together: a rule the agent runs, bound to a sidecar listener through
// sidecar_targets, is served translated, refused at the save with 422, or
// refused at the handshake as not_served when the sidecar changed under it.
package gatewayrulestest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	apidatamasking "github.com/hoophq/hoop/gateway/api/datamasking"
	apiguardrails "github.com/hoophq/hoop/gateway/api/guardrails"
	"github.com/hoophq/hoop/gateway/api/openapi"
	apisidecar "github.com/hoophq/hoop/gateway/api/sidecar"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite/pglitetest"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/sidecar/daemon"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const orgID = "00000000-0000-0000-0000-0000000000d4"

func TestMain(m *testing.M) {
	// The data masking API needs a DLP provider for a rule the agent runs;
	// alcatraz needs no credentials.
	os.Setenv("DLP_PROVIDER", "alcatraz")
	if err := appconfig.Load(); err != nil {
		panic(err)
	}
	os.Exit(pglitetest.Main(m))
}

func startDB(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	inst := pglitetest.StartMigrated(t)
	require.NoError(t, models.InitDatabaseConnection(inst.DSN(), 1))
	require.NoError(t, models.DB.Exec(`INSERT INTO private.orgs (id, name) VALUES (?, 'gatewayrules-test')`, orgID).Error)
}

// sidecarWithListener is a sidecar with one postgres listener named appdb.
func sidecarWithListener(t *testing.T, name string) *models.Sidecar {
	t.Helper()
	sc := &models.Sidecar{OrgID: orgID, Name: name, KeyHash: models.HashAPIKey("hsc_" + name), CreatedBy: "tests@hoop.dev"}
	require.NoError(t, models.CreateSidecar(models.DB, sc))
	cfg, err := services.ParseSidecarConfiguration([]byte(`{"listeners":[{"name":"appdb","protocol":"postgres",
		"listen":":5432","upstream":"db:5432"}]}`))
	require.NoError(t, err)
	stored, err := models.UpdateSidecarConfiguration(models.DB, orgID, sc.ID, models.SidecarConfiguration(cfg))
	require.NoError(t, err)
	return stored
}

// agentConnection is a plain connection an agent serves, the kind a gateway
// rule binds to with connection_ids.
func agentConnection(t *testing.T, name string) string {
	t.Helper()
	id := uuid.NewString()
	require.NoError(t, models.DB.Exec(`INSERT INTO private.resources (org_id, name, type, subtype)
		VALUES (?, ?, 'database', 'postgres')`, orgID, name).Error)
	require.NoError(t, models.DB.Exec(`INSERT INTO private.connections (id, org_id, name, type, resource_name)
		VALUES (?, ?, ?, 'postgres', ?)`, id, orgID, name, name).Error)
	return id
}

// licenseDoc lifts the OSS one-rule cap of the rule APIs, which these tests
// do not exercise. The sidecar caps still read the organization row.
const licenseDoc = `{"payload":{"type":"enterprise","issued_at":1,"expire_at":2,` +
	`"allowed_hosts":["*"],"description":"Acme"},"key_id":"k1","signature":"c2ln"}`

func call(t *testing.T, handler gin.HandlerFunc, method, path string, body any, params ...gin.Param) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, path, bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(storagev2.ContextKey, storagev2.NewContext("user-1", orgID).
		WithUserInfo("Admin", "admin@hoop.dev", "active", "", nil).
		WithOrgLicenseData(json.RawMessage(licenseDoc)))
	c.Params = params
	handler(c)
	return w
}

func handshake(t *testing.T, sc *models.Sidecar) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/sidecars/handshake", strings.NewReader(`{"version":"1.200.0"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("sidecar-auth", sc)
	apisidecar.Handshake(c)
	return w
}

func denyWords(words ...string) map[string]any {
	return map[string]any{"rules": []any{map[string]any{"type": "deny_words_list", "words": words, "pattern_regex": ""}}}
}

func patternRules(regex string) map[string]any {
	return map[string]any{"rules": []any{map[string]any{"type": "pattern_match", "words": []any{}, "pattern_regex": regex}}}
}

var noRules = map[string]any{"rules": []any{}}

func targets(sc *models.Sidecar) []map[string]any {
	return []map[string]any{{"sidecar_id": sc.ID, "listener_name": "appdb"}}
}

func guardrailRequest(name string, input, output map[string]any, connectionIDs []string, sidecarTargets []map[string]any) map[string]any {
	req := map[string]any{"name": name, "description": "", "input": input, "output": output,
		"connection_ids": connectionIDs, "attributes": []string{}}
	if sidecarTargets != nil {
		req["sidecar_targets"] = sidecarTargets
	}
	return req
}

func maskingRequest(name string, entities []string, custom []map[string]any, connectionIDs []string, sidecarTargets []map[string]any) map[string]any {
	supported := []map[string]any{}
	if len(entities) > 0 {
		supported = append(supported, map[string]any{"name": "PII", "entity_types": entities})
	}
	req := map[string]any{"name": name, "description": "",
		"supported_entity_types": supported,
		"custom_entity_types":    custom,
		"connection_ids":         connectionIDs, "attributes": []string{}}
	if sidecarTargets != nil {
		req["sidecar_targets"] = sidecarTargets
	}
	return req
}

func createdID(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	return created.ID
}

// A guardrail the agent runs, bound to a listener through sidecar_targets,
// reaches the sidecar translated; its agent binding stays; the Sidecars page
// lists it with source gateway.
func TestAnAgentGuardrailBoundToAListenerIsServedTranslated(t *testing.T) {
	startDB(t)
	sc := sidecarWithListener(t, "served")
	conn := agentConnection(t, "pg-prod")
	w := call(t, apiguardrails.Post, http.MethodPost, "/api/guardrails",
		guardrailRequest("no-drop", denyWords("DROP TABLE"), noRules, []string{conn}, targets(sc)))
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body)
	id := createdID(t, w)

	w = handshake(t, sc)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	var served daemon.Config
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &served))
	require.Len(t, served.Listeners, 1)
	require.NotNil(t, served.Listeners[0].Guardrails)
	require.Len(t, served.Listeners[0].Guardrails.Rules, 1)
	rule := served.Listeners[0].Guardrails.Rules[0]
	assert.Equal(t, "no-drop", rule.Name)
	assert.Equal(t, "pattern_match", string(rule.Type))
	assert.Equal(t, "DROP TABLE", rule.Pattern)

	row, err := models.GetSidecarByNameOrID(models.DB, orgID, sc.ID)
	require.NoError(t, err)
	assert.Nil(t, row.LastError)

	// The rule keeps its agent binding and its targets, and stores no
	// sidecar_spec: the translation is derived, never written.
	w = call(t, apiguardrails.Get, http.MethodGet, "/api/guardrails/"+id, nil, gin.Param{Key: "id", Value: id})
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	var got openapi.GuardRailRuleResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, []string{conn}, got.ConnectionIDs)
	assert.Equal(t, []openapi.SidecarRuleTarget{{SidecarID: sc.ID, ListenerName: "appdb"}}, got.SidecarTargets)
	assert.Empty(t, got.SidecarSpec)

	w = call(t, apisidecar.Get, http.MethodGet, "/api/sidecars/"+sc.ID, nil, gin.Param{Key: "nameOrID", Value: sc.ID})
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	var page openapi.SidecarResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
	assert.False(t, page.BoundRulesUnavailable)
	assert.Equal(t, []openapi.SidecarRuleBinding{
		{Kind: "guardrail", RuleName: "no-drop", ListenerName: "appdb", Source: openapi.SidecarRuleSourceGateway},
	}, page.BoundRules)
}

// A rule the sidecar cannot run is refused at the save, with the reason, and
// nothing of it is stored. An edit that makes a served rule untranslatable
// is refused the same way, and the stored rule goes on serving.
func TestAnUntranslatableRuleIsRefusedAtTheSave(t *testing.T) {
	startDB(t)
	sc := sidecarWithListener(t, "refused")

	w := call(t, apiguardrails.Post, http.MethodPost, "/api/guardrails",
		guardrailRequest("mask-ssn", denyWords("x"), patternRules(`\d{3}-\d{2}-\d{4}`), []string{}, targets(sc)))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body: %s", w.Body)
	assert.Contains(t, w.Body.String(), `guardrail rule \"mask-ssn\", bound to listener \"appdb\", has output rules`)
	rules, err := models.ListGuardRailRules(orgID)
	require.NoError(t, err)
	assert.Empty(t, rules, "a refused rule is not saved")

	w = call(t, apidatamasking.Post, http.MethodPost, "/api/datamasking-rules",
		maskingRequest("ids", nil,
			[]map[string]any{{"name": "ORDER_ID", "regex": `ORD-\d+`, "deny_list": []string{}, "score": 0.9}}, []string{}, targets(sc)))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body: %s", w.Body)
	assert.Contains(t, w.Body.String(), "has custom entity types")
	masks, err := models.ListDataMaskingRules(orgID)
	require.NoError(t, err)
	assert.Empty(t, masks, "a refused rule is not saved")

	// The same rules save when they name no sidecar: the agent runs them.
	w = call(t, apiguardrails.Post, http.MethodPost, "/api/guardrails",
		guardrailRequest("agent-only", denyWords("x"), patternRules("secret"), []string{}, nil))
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body)

	w = call(t, apiguardrails.Post, http.MethodPost, "/api/guardrails",
		guardrailRequest("no-drop", denyWords("DROP TABLE"), noRules, []string{}, targets(sc)))
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body)
	id := createdID(t, w)
	w = call(t, apiguardrails.Put, http.MethodPut, "/api/guardrails/"+id,
		guardrailRequest("no-drop", denyWords("DROP TABLE"), patternRules("secret"), []string{}, targets(sc)),
		gin.Param{Key: "id", Value: id})
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body: %s", w.Body)
	assert.Contains(t, w.Body.String(), "has output rules")

	w = handshake(t, sc)
	require.Equal(t, http.StatusOK, w.Code, "the stored rule still serves: %s", w.Body)
	assert.Contains(t, w.Body.String(), "DROP TABLE")
	assert.NotContains(t, w.Body.String(), "secret")
}

// A masking rule the agent runs, bound to a listener, masks there with the
// sidecar's detector; a sidecar whose pii section changes under it (here:
// written past the configuration API, which refuses the edit) is refused at
// the handshake, keeps its rules, and the page names the reason.
func TestAMaskingRuleIsServedUntilThePiiSectionDropsItsEntity(t *testing.T) {
	startDB(t)
	sc := sidecarWithListener(t, "pii")
	w := call(t, apidatamasking.Post, http.MethodPost, "/api/datamasking-rules",
		maskingRequest("emails", []string{"EMAIL_ADDRESS"}, nil, []string{}, targets(sc)))
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body)

	w = handshake(t, sc)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	var served daemon.Config
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &served))
	require.NotNil(t, served.Listeners[0].Mask)
	assert.Contains(t, string(served.Listeners[0].Mask.Rules), `"EMAIL_ADDRESS"`)
	assert.Contains(t, string(served.Listeners[0].Mask.Rules), `"strategy":"mask"`)

	// The configuration API refuses the edit that breaks the bound rule.
	narrowed := *sc
	narrowed.Configuration.PII = json.RawMessage(`{"entities":["US_SSN"]}`)
	err := services.CheckComposedSidecarConfiguration(models.DB, &narrowed)
	var broken services.ErrSidecarBindingBroken
	require.ErrorAs(t, err, &broken)
	assert.Contains(t, err.Error(), `names entity "EMAIL_ADDRESS"`)

	// Written anyway: the handshake is the backstop.
	stored, err := models.UpdateSidecarConfiguration(models.DB, orgID, sc.ID, narrowed.Configuration)
	require.NoError(t, err)
	w = handshake(t, stored)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body: %s", w.Body)
	assert.Contains(t, w.Body.String(), `names entity \"EMAIL_ADDRESS\"`)
	row, err := models.GetSidecarByNameOrID(models.DB, orgID, sc.ID)
	require.NoError(t, err)
	require.NotNil(t, row.LastOutcome)
	assert.Equal(t, models.SidecarOutcomeNotServed, *row.LastOutcome)
	require.NotNil(t, row.LastError)
	assert.Contains(t, *row.LastError, `names entity "EMAIL_ADDRESS"`)

	// Unbinding the rule serves the sidecar again and clears the verdict.
	masks, err := models.ListDataMaskingRules(orgID)
	require.NoError(t, err)
	require.Len(t, masks, 1)
	w = call(t, apidatamasking.Put, http.MethodPut, "/api/datamasking-rules/"+masks[0].ID,
		maskingRequest("emails", []string{"EMAIL_ADDRESS"}, nil, []string{}, []map[string]any{}),
		gin.Param{Key: "id", Value: masks[0].ID})
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	w = handshake(t, stored)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	row, err = models.GetSidecarByNameOrID(models.DB, orgID, sc.ID)
	require.NoError(t, err)
	assert.Nil(t, row.LastError)
}
