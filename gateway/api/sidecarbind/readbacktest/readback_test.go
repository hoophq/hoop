package readbacktest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	apiai "github.com/hoophq/hoop/gateway/api/ai"
	apidatamasking "github.com/hoophq/hoop/gateway/api/datamasking"
	apiguardrails "github.com/hoophq/hoop/gateway/api/guardrails"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	modelsbootstrap "github.com/hoophq/hoop/gateway/models/bootstrap"
	"github.com/hoophq/hoop/gateway/pglite"
	"github.com/hoophq/hoop/gateway/storagev2"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const orgID = "00000000-0000-0000-0000-0000000000c3"

func TestMain(m *testing.M) {
	if err := appconfig.Load(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

// rule is one rule of each kind, bound to the appdb listener of one sidecar.
type rule struct {
	handler     gin.HandlerFunc
	param, key  string
	listenersOf string
}

// seed boots the embedded database the way the gateway does, then writes one
// sidecar and one rule of each kind bound to its appdb listener.
func seed(t *testing.T) map[string]rule {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	ctx := context.Background()
	inst, err := pglite.Start(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { inst.Close(ctx) })
	require.NoError(t, modelsbootstrap.MigrateDB(inst.MigrateDSN(), ""))
	// The embedded backend serves one session at a time.
	require.NoError(t, models.InitDatabaseConnection(inst.DSN(), 1))
	require.NoError(t, models.DB.Exec(`INSERT INTO private.orgs (id, name) VALUES (?, 'readback-test')`, orgID).Error)

	sc := &models.Sidecar{OrgID: orgID, Name: "payments", KeyHash: models.HashAPIKey("hsc_payments"), CreatedBy: "tests@hoop.dev"}
	require.NoError(t, models.CreateSidecar(models.DB, sc))
	org := uuid.MustParse(orgID)
	target := []models.SidecarRuleTarget{{SidecarID: sc.ID, ListenerName: "appdb"}}
	now := time.Now().UTC()

	guardrail := &models.GuardRailRules{
		ID: uuid.NewString(), OrgID: orgID, Name: "no-drop", CreatedAt: now, UpdatedAt: now,
		Input: map[string]any{}, Output: map[string]any{},
		SidecarSpec: json.RawMessage(`{"rules":[{"name":"d","type":"deny_words_list","words":["drop"]}]}`),
	}
	require.NoError(t, models.UpsertGuardRailRuleWithConnections(guardrail, nil, true))
	require.NoError(t, models.SetGuardrailRuleListenersTx(models.DB, org, guardrail.Name, target))

	mask := &models.DataMaskingRule{
		ID: uuid.NewString(), OrgID: orgID, Name: "mask-pii", UpdatedAt: now,
		SupportedEntityTypes: models.SupportedEntityTypesList{}, CustomEntityTypes: models.CustomEntityTypesList{},
		SidecarSpec: json.RawMessage(`{"rules":[{"name":"e","entities":["EMAIL_ADDRESS"],"strategy":"redact"}]}`),
	}
	_, err = models.CreateDataMaskingRule(mask)
	require.NoError(t, err)
	require.NoError(t, models.SetDataMaskingRuleListenersTx(models.DB, org, mask.Name, target))

	analyzer := &models.AISessionAnalyzerRules{
		OrgID: org, Name: "risky", ConnectionNames: []string{},
		SidecarSpec: json.RawMessage(`{"high":"block"}`),
		RiskEvaluation: models.AISessionAnalyzerRiskEvaluation{
			LowRisk:    &models.AISessionAnalyzerRiskTier{Action: models.AllowExecution},
			MediumRisk: &models.AISessionAnalyzerRiskTier{Action: models.AllowExecution},
			HighRisk:   &models.AISessionAnalyzerRiskTier{Action: models.AllowExecution},
		},
	}
	require.NoError(t, models.CreateAISessionAnalyzerRuleTx(models.DB, analyzer))
	require.NoError(t, models.SetAnalyzerRuleListenersTx(models.DB, org, analyzer.Name, target))

	return map[string]rule{
		"guardrail":    {apiguardrails.Get, "id", guardrail.ID, "private.guardrail_rules_listeners"},
		"data masking": {apidatamasking.Get, "id", mask.ID, "private.datamasking_rules_listeners"},
		"analyzer":     {apiai.GetSessionAnalyzerRule, "name", analyzer.Name, "private.ai_session_analyzer_rules_listeners"},
	}
}

func get(t *testing.T, r rule) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Set(storagev2.ContextKey, storagev2.NewContext("user-1", orgID).
		WithUserInfo("Admin", "admin@hoop.dev", "active", "", nil))
	c.Params = gin.Params{{Key: r.param, Value: r.key}}
	r.handler(c)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), "body: %s", w.Body)
	return w, body
}

// The edit form opens with what these routes answer, and saves it back.
func TestARuleReadsBackWhereItIsBound(t *testing.T) {
	for kind, r := range seed(t) {
		t.Run(kind, func(t *testing.T) {
			w, body := get(t, r)
			require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
			targets, _ := body["sidecar_targets"].([]any)
			require.Len(t, targets, 1, "body: %s", w.Body)
			assert.Equal(t, "appdb", targets[0].(map[string]any)["listener_name"])
		})
	}
}

// A rule whose bindings cannot be read is not served as bound nowhere: the
// form would open with no listener, and its next save would unbind the rule
// from every sidecar it reached.
func TestARuleWhoseBindingsCannotBeReadIsNotServedUnbound(t *testing.T) {
	rules := seed(t)
	for kind, r := range rules {
		require.NoError(t, models.DB.Exec(`DROP TABLE `+r.listenersOf).Error, kind)
	}
	for kind, r := range rules {
		t.Run(kind, func(t *testing.T) {
			w, body := get(t, r)
			assert.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body)
			assert.NotContains(t, body, "sidecar_targets")
		})
	}
}
