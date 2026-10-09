package apisidecar

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The sidecar sets no limit on a listener name, and 1.214.0 stored a
// 300-character one. It keeps a mirror and every feature that reads a listener.
func TestAListenerNameOver255CharactersIsMirrored(t *testing.T) {
	startSwitchDB(t)
	seedApprovalRuleIn(t, switchOrgID)
	long := strings.Repeat("l", 300)
	cfg := `{"analyzer": {"provider": "anthropic", "model": "m"}, "listeners": [
		{"name": "appdb", "protocol": "postgres", "listen": ":5432", "upstream": "db:5432"},
		{"name": "` + long + `", "protocol": "postgres", "listen": ":5433", "upstream": "db:5432",
		 "analyzer": {"high": "require_review", "approval_rule": "payments-approvers"}}]}`

	w, created := postSidecar(t, "edge3", cfg)
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body)
	sc, err := models.GetSidecarByNameOrID(models.DB, switchOrgID, created.ID)
	require.NoError(t, err)
	mirror, err := models.GetSidecarMirror(models.DB, switchOrgID, sc.ID, long)
	require.NoError(t, err)
	require.Equal(t, models.SidecarMirrorFallbackName("edge3-"+long, sc.ID, long), mirror.Name)

	t.Run("the sidecar can be edited", func(t *testing.T) {
		w, _ := callAdmin(t, Put, http.MethodPut, sc.ID, cfg)
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
		got, err := models.GetSidecarMirror(models.DB, switchOrgID, sc.ID, long)
		require.NoError(t, err)
		assert.Equal(t, mirror.ID, got.ID, "the edit keeps the mirror")
	})

	t.Run("the startup reconcile mirrors it after the upgrade", func(t *testing.T) {
		// As 1.214.0 left it: the listener stored, no mirror.
		require.NoError(t, models.DB.Exec(`DELETE FROM private.connections WHERE org_id = ? AND id = ?`, switchOrgID, mirror.ID).Error)
		require.NoError(t, models.DB.Exec(`DELETE FROM private.resources WHERE org_id = ? AND name = ?`, switchOrgID, mirror.Name).Error)
		services.ReconcileAllSidecarListenerConnections(models.DB)
		got, err := models.GetSidecarMirror(models.DB, switchOrgID, sc.ID, long)
		require.NoError(t, err)
		assert.Equal(t, mirror.Name, got.Name)
		mirror = got
	})

	t.Run("rules bind to it", func(t *testing.T) {
		org := uuid.MustParse(switchOrgID)
		target := []models.SidecarRuleTarget{{SidecarID: sc.ID, ListenerName: long}}
		now := time.Now().UTC()
		guardrail := &models.GuardRailRules{ID: uuid.NewString(), OrgID: switchOrgID, Name: "no-drop",
			CreatedAt: now, UpdatedAt: now, Input: map[string]any{}, Output: map[string]any{},
			SidecarSpec: json.RawMessage(`{"rules":[{"name":"d","type":"deny_words_list","words":["drop"]}]}`)}
		require.NoError(t, models.UpsertGuardRailRuleWithConnections(guardrail, nil, true))
		require.NoError(t, models.SetGuardrailRuleListenersTx(models.DB, org, guardrail.Name, target))
		mask := &models.DataMaskingRule{ID: uuid.NewString(), OrgID: switchOrgID, Name: "mask-pii", UpdatedAt: now,
			SupportedEntityTypes: models.SupportedEntityTypesList{}, CustomEntityTypes: models.CustomEntityTypesList{},
			SidecarSpec: json.RawMessage(`{"rules":[{"name":"e","entities":["EMAIL_ADDRESS"],"strategy":"redact"}]}`)}
		_, err := models.CreateDataMaskingRule(mask)
		require.NoError(t, err)
		require.NoError(t, models.SetDataMaskingRuleListenersTx(models.DB, org, mask.Name, target))
		analyzer := &models.AISessionAnalyzerRules{OrgID: org, Name: "risky", ConnectionNames: []string{},
			SidecarSpec: json.RawMessage(`{"high":"block"}`),
			RiskEvaluation: models.AISessionAnalyzerRiskEvaluation{
				LowRisk:    &models.AISessionAnalyzerRiskTier{Action: models.AllowExecution},
				MediumRisk: &models.AISessionAnalyzerRiskTier{Action: models.AllowExecution},
				HighRisk:   &models.AISessionAnalyzerRiskTier{Action: models.AllowExecution},
			}}
		require.NoError(t, models.CreateAISessionAnalyzerRuleTx(models.DB, analyzer))
		require.NoError(t, models.SetAnalyzerRuleListenersTx(models.DB, org, analyzer.Name, target))

		for kind, list := range map[string]func() ([]models.BoundRule, error){
			"guardrail": func() ([]models.BoundRule, error) { return models.ListGuardrailRulesForSidecar(models.DB, org, sc.ID) },
			"data masking": func() ([]models.BoundRule, error) {
				return models.ListDataMaskingRulesForSidecar(models.DB, org, sc.ID)
			},
			"analyzer": func() ([]models.BoundRule, error) { return models.ListAnalyzerRulesForSidecar(models.DB, org, sc.ID) },
		} {
			bound, err := list()
			require.NoError(t, err, kind)
			require.Len(t, bound, 1, kind)
			assert.Equal(t, long, bound[0].ListenerName, kind)
		}

		served, err := services.ComposeSidecarConfiguration(models.DB, sc)
		require.NoError(t, err)
		require.NotNil(t, served.Listeners[1].Guardrails, "the guardrail reaches the lane")
		assert.Len(t, served.Listeners[1].Guardrails.Rules, 1)
	})

	t.Run("its Slack channels are stored", func(t *testing.T) {
		body := `{"listeners": [{"name": "` + long + `", "channels": ["C0123"]}]}`
		w := putSlackChannels(t, sc.ID, body)
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
		rows, err := models.ListSidecarSlackChannels(models.DB, switchOrgID, sc.ID)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		assert.Equal(t, long, rows[0].ListenerName)
	})

	t.Run("a review is filed on it", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Set("sidecar-auth", sc)
		body, _ := json.Marshal(map[string]string{"listener_name": long, "approval_rule": "payments-approvers",
			"payload": base64.StdEncoding.EncodeToString([]byte("DELETE FROM users;"))})
		c.Request = httptest.NewRequest(http.MethodPost, "/api/sidecars/reviews", bytes.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		PostReview(c)
		require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body)

		var stored struct{ ListenerName, ConnectionName string }
		require.NoError(t, models.DB.Raw(`SELECT listener_name, connection_name FROM private.reviews
			WHERE org_id = ? AND sidecar_id = ?`, switchOrgID, sc.ID).Scan(&stored).Error)
		assert.Equal(t, long, stored.ListenerName)
		assert.Equal(t, mirror.Name, stored.ConnectionName)
	})

	t.Run("a session is recorded under its mirror", func(t *testing.T) {
		enableSessionEvents(t, switchOrgID)
		got := decodeEventsResponse(t, postEvents(sc, eventsBody(t,
			sessionEvent(1, "s-long", 0, audit.KindSessionStart, func(e *audit.Event) { e.Connection = long }))))
		assert.Equal(t, 1, got.Accepted)
		row := getSidecarSessionRow(t, switchOrgID, services.SidecarSessionID(sc.ID, "s-long"))
		assert.Equal(t, mirror.Name, row.Connection)
		assert.Equal(t, long, sidecarMetadata(t, row)["listener"])
	})
}

func putSlackChannels(t *testing.T, nameOrID, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/sidecars/"+nameOrID+"/slack-channels", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(storagev2.ContextKey, storagev2.NewContext("user-1", switchOrgID).
		WithUserInfo("Admin", "admin@hoop.dev", "active", "", nil))
	c.Params = gin.Params{{Key: "nameOrID", Value: nameOrID}}
	PutSlackChannels(c)
	return w
}
