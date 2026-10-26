// Package gatewayrulestest drives the rule APIs and the sidecar handshake
// together: a gateway rule bound to a sidecar's mirror connection is served
// translated, refused at the save with 422, or refused at the handshake as
// not_served when it was bound another way.
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
	"gorm.io/gorm"
)

const orgID = "00000000-0000-0000-0000-0000000000d4"

func TestMain(m *testing.M) {
	// The data masking API needs a DLP provider for a rule bound to
	// connections; alcatraz needs no credentials.
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

// mirroredSidecar is a sidecar with one postgres listener and its mirror.
func mirroredSidecar(t *testing.T, name string) (*models.Sidecar, *models.SidecarMirror) {
	t.Helper()
	sc := &models.Sidecar{OrgID: orgID, Name: name, KeyHash: models.HashAPIKey("hsc_" + name), CreatedBy: "tests@hoop.dev"}
	require.NoError(t, models.CreateSidecar(models.DB, sc))
	cfg, err := services.ParseSidecarConfiguration([]byte(`{"listeners":[{"name":"appdb","protocol":"postgres",
		"listen":":5432","upstream":"db:5432"}]}`))
	require.NoError(t, err)
	stored, err := models.UpdateSidecarConfiguration(models.DB, orgID, sc.ID, models.SidecarConfiguration(cfg))
	require.NoError(t, err)
	require.NoError(t, models.DB.Transaction(func(tx *gorm.DB) error {
		return services.SyncSidecarListenerConnectionsTx(tx, stored)
	}))
	mirror, err := models.GetSidecarMirror(models.DB, orgID, sc.ID, "appdb")
	require.NoError(t, err)
	return stored, mirror
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

func guardrailRequest(name string, input, output map[string]any, connectionIDs []string) map[string]any {
	return map[string]any{"name": name, "description": "", "input": input, "output": output,
		"connection_ids": connectionIDs, "attributes": []string{}}
}

func maskingRequest(name string, entities []string, custom []map[string]any, connectionIDs []string) map[string]any {
	supported := []map[string]any{}
	if len(entities) > 0 {
		supported = append(supported, map[string]any{"name": "PII", "entity_types": entities})
	}
	return map[string]any{"name": name, "description": "",
		"supported_entity_types": supported,
		"custom_entity_types":    custom,
		"connection_ids":         connectionIDs, "attributes": []string{}}
}

// Done when #1: a guardrail created on the gateway page, bound to the
// mirror, reaches the sidecar translated, after the listener's own rules.
func TestAGatewayGuardrailIsServedTranslated(t *testing.T) {
	startDB(t)
	sc, mirror := mirroredSidecar(t, "served")
	w := call(t, apiguardrails.Post, http.MethodPost, "/api/guardrails",
		guardrailRequest("no-drop", denyWords("DROP TABLE"), map[string]any{"rules": []any{}}, []string{mirror.ID}))
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body)

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

	// The Sidecars page lists what the listener enforces, the translated
	// rule included, with the source that tells it from a native rule.
	w = call(t, apisidecar.Get, http.MethodGet, "/api/sidecars/"+sc.ID, nil, gin.Param{Key: "nameOrID", Value: sc.ID})
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	var page openapi.SidecarResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
	assert.False(t, page.BoundRulesUnavailable)
	assert.Equal(t, []openapi.SidecarRuleBinding{
		{Kind: "guardrail", RuleName: "no-drop", ListenerName: "appdb", Source: openapi.SidecarRuleSourceGateway},
	}, page.BoundRules)
}

// A sidecar another rule already breaks (here: an output rule bound by
// attribute, which no save gates) does not refuse the writes of the rules
// that reach it: the gate refuses only the rule that breaks it.
func TestTheGateRefusesOnlyTheRuleBeingWritten(t *testing.T) {
	startDB(t)
	sc, mirror := mirroredSidecar(t, "crossrule")
	w := call(t, apiguardrails.Post, http.MethodPost, "/api/guardrails",
		map[string]any{"name": "ssn-out", "input": map[string]any{"rules": []any{}},
			"output": patternRules(`\d{3}-\d{2}-\d{4}`), "connection_ids": []string{}, "attributes": []string{"prod"}})
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body)
	require.NoError(t, models.UpsertConnectionAttributes(models.DB, uuid.MustParse(orgID), mirror.Name, []string{"prod"}))
	w = handshake(t, sc)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "the sidecar is broken by ssn-out: %s", w.Body)

	w = call(t, apiguardrails.Post, http.MethodPost, "/api/guardrails",
		guardrailRequest("no-drop", denyWords("DROP TABLE"), map[string]any{"rules": []any{}}, []string{mirror.ID}))
	require.Equal(t, http.StatusCreated, w.Code, "an unrelated rule the sidecar runs saves: %s", w.Body)
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	req := guardrailRequest("no-drop", denyWords("DROP TABLE"), map[string]any{"rules": []any{}}, []string{mirror.ID})
	req["description"] = "edited"
	w = call(t, apiguardrails.Put, http.MethodPut, "/api/guardrails/"+created.ID, req, gin.Param{Key: "id", Value: created.ID})
	require.Equal(t, http.StatusOK, w.Code, "an edit of that rule saves: %s", w.Body)

	w = call(t, apidatamasking.Post, http.MethodPost, "/api/datamasking-rules",
		maskingRequest("emails", []string{"EMAIL_ADDRESS"}, nil, []string{mirror.ID}))
	require.Equal(t, http.StatusCreated, w.Code, "a masking rule the sidecar runs saves: %s", w.Body)

	// A rule that breaks the sidecar on its own is still refused, with ITS reason.
	w = call(t, apiguardrails.Post, http.MethodPost, "/api/guardrails",
		guardrailRequest("mask-ssn", denyWords("x"), patternRules("secret"), []string{mirror.ID}))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body: %s", w.Body)
	assert.Contains(t, w.Body.String(), `guardrail rule \"mask-ssn\", reaching listener \"appdb\", has output rules`)
	assert.NotContains(t, w.Body.String(), "ssn-out")

	// Once the other rule is unbound, every rule reaching the listener serves.
	require.NoError(t, models.UpsertGuardrailRuleAttributes(models.DB, uuid.MustParse(orgID), "ssn-out", nil))
	w = handshake(t, sc)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	assert.Contains(t, w.Body.String(), "DROP TABLE")
	assert.Contains(t, w.Body.String(), "EMAIL_ADDRESS")
}

// Done when #2: a rule the sidecar cannot run is refused at the save, with
// the reason, and nothing of it is stored.
func TestAnUntranslatableRuleIsRefusedAtTheSave(t *testing.T) {
	startDB(t)
	sc, mirror := mirroredSidecar(t, "refused")

	w := call(t, apiguardrails.Post, http.MethodPost, "/api/guardrails",
		guardrailRequest("mask-ssn", denyWords("x"), patternRules(`\d{3}-\d{2}-\d{4}`), []string{mirror.ID}))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body: %s", w.Body)
	assert.Contains(t, w.Body.String(), `guardrail rule \"mask-ssn\", reaching listener \"appdb\", has output rules`)
	rules, err := models.ListGuardRailRules(orgID)
	require.NoError(t, err)
	assert.Empty(t, rules, "a refused rule is not saved")

	w = call(t, apidatamasking.Post, http.MethodPost, "/api/datamasking-rules",
		maskingRequest("ids", nil,
			[]map[string]any{{"name": "ORDER_ID", "regex": `ORD-\d+`, "deny_list": []string{}, "score": 0.9}}, []string{mirror.ID}))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body: %s", w.Body)
	assert.Contains(t, w.Body.String(), "has custom entity types")
	masks, err := models.ListDataMaskingRules(orgID)
	require.NoError(t, err)
	assert.Empty(t, masks, "a refused rule is not saved")

	// An edit that makes a served rule untranslatable is refused the same way.
	w = call(t, apiguardrails.Post, http.MethodPost, "/api/guardrails",
		guardrailRequest("no-drop", denyWords("DROP TABLE"), map[string]any{"rules": []any{}}, []string{mirror.ID}))
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body)
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	w = call(t, apiguardrails.Put, http.MethodPut, "/api/guardrails/"+created.ID,
		guardrailRequest("no-drop", denyWords("DROP TABLE"), patternRules("secret"), []string{mirror.ID}),
		gin.Param{Key: "id", Value: created.ID})
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body: %s", w.Body)
	assert.Contains(t, w.Body.String(), "has output rules")

	w = handshake(t, sc)
	require.Equal(t, http.StatusOK, w.Code, "the stored rule still serves: %s", w.Body)
	assert.NotContains(t, w.Body.String(), "secret")
}

// A rule bound another way (here: the mirror gains the rule's attribute
// through the connection side) is refused at the handshake: the sidecar
// keeps its rules, and the page shows not_served with the reason.
func TestARuleBoundAnotherWayIsRefusedAtTheHandshake(t *testing.T) {
	startDB(t)
	sc, mirror := mirroredSidecar(t, "attr")
	w := call(t, apiguardrails.Post, http.MethodPost, "/api/guardrails",
		map[string]any{"name": "ssn-out", "input": map[string]any{"rules": []any{}},
			"output": patternRules(`\d{3}-\d{2}-\d{4}`), "connection_ids": []string{}, "attributes": []string{"prod"}})
	require.Equal(t, http.StatusCreated, w.Code, "a rule reaching no sidecar saves: %s", w.Body)

	w = handshake(t, sc)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)

	require.NoError(t, models.UpsertConnectionAttributes(models.DB, uuid.MustParse(orgID), mirror.Name, []string{"prod"}))
	w = handshake(t, sc)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body: %s", w.Body)
	assert.Contains(t, w.Body.String(), `guardrail rule \"ssn-out\", reaching listener \"appdb\", has output rules`)

	row, err := models.GetSidecarByNameOrID(models.DB, orgID, sc.ID)
	require.NoError(t, err)
	require.NotNil(t, row.LastOutcome)
	assert.Equal(t, models.SidecarOutcomeNotServed, *row.LastOutcome)
	require.NotNil(t, row.LastError)
	assert.Contains(t, *row.LastError, "has output rules")

	// Removing the attribute from the rule serves the sidecar again and
	// clears the verdict.
	require.NoError(t, models.UpsertGuardrailRuleAttributes(models.DB, uuid.MustParse(orgID), "ssn-out", nil))
	w = handshake(t, sc)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	row, err = models.GetSidecarByNameOrID(models.DB, orgID, sc.ID)
	require.NoError(t, err)
	assert.Nil(t, row.LastError)
}
