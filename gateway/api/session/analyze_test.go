package sessionapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite/pglitetest"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) { os.Exit(pglitetest.Main(m)) }

const (
	analyzeTestOrgID = "00000000-0000-0000-0000-0000000000e6"
	analyzeTestConn  = "pg-gmud"
	// analyzeMetaConn requires the change_ticket metadata field.
	analyzeMetaConn = "pg-meta"
)

// The rule of the test org: medium risk needs approval, high risk blocks.
const defaultRiskEvaluation = `{"low_risk":{"action":"allow_execution"},
	"medium_risk":{"action":"require_access_request","access_request_rule_name":"gmud-approval"},
	"high_risk":{"action":"block_execution"}}`

// llmStub answers as an OpenAI-compatible provider: a script with DROP is high
// risk, UPDATE medium, anything else low. calls counts the analyzer runs.
func llmStub(t *testing.T, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("llm stub: read request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		tool, title := "LowRiskAISessionAnalyzer", "Read only"
		switch {
		case strings.Contains(string(body), "DROP"):
			tool, title = "HighRiskAISessionAnalyzer", "Drops a table"
		case strings.Contains(string(body), "UPDATE"):
			tool, title = "MediumRiskAISessionAnalyzer", "Changes rows"
		}
		args, err := json.Marshal(map[string]string{"title": title, "explanation": title + "."})
		if err != nil {
			t.Errorf("llm stub: encode arguments: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := fmt.Fprintf(w, `{"id":"c1","object":"chat.completion","created":0,"model":"stub",
			"choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","content":"",
			"tool_calls":[{"id":"t1","type":"function","function":{"name":%q,"arguments":%q}}]}}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`, tool, string(args)); err != nil {
			t.Errorf("llm stub: write response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// startAnalyzeTestDB seeds an org, two connections, an AI provider that points
// at the stub, and an analyzer rule for both connections.
func startAnalyzeTestDB(t *testing.T, llmURL string) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	inst := pglitetest.StartMigrated(t)
	require.NoError(t, models.InitDatabaseConnection(inst.DSN(), 1))
	exec := func(query string, args ...any) {
		t.Helper()
		require.NoError(t, models.DB.Exec(query, args...).Error)
	}
	exec(`INSERT INTO private.orgs (id, name) VALUES (?, 'analyze-test')`, analyzeTestOrgID)
	for _, name := range []string{analyzeTestConn, analyzeMetaConn} {
		exec(`INSERT INTO private.resources (org_id, name, type, subtype) VALUES (?, ?, 'database', 'postgres')`,
			analyzeTestOrgID, name)
		exec(`INSERT INTO private.connections (org_id, name, type, subtype, resource_name, access_mode_exec)
			VALUES (?, ?, 'database', 'postgres', ?, 'enabled'::private.enum_access_status)`,
			analyzeTestOrgID, name, name)
	}
	exec(`UPDATE private.connections SET mandatory_metadata_fields = ARRAY['change_ticket'] WHERE name = ?`, analyzeMetaConn)
	exec(`INSERT INTO private.ai_providers (org_id, feature, provider, api_url, api_key, model)
		VALUES (?, 'session-analyzer', 'openai', ?, 'stub-key', 'stub')`, analyzeTestOrgID, llmURL+"/v1/")
	require.NoError(t, models.DB.Create(&models.AccessRequestRule{
		OrgID:                  uuid.MustParse(analyzeTestOrgID),
		Name:                   "gmud-approval",
		AccessType:             models.AccessTypeCommand,
		ConnectionNames:        pq.StringArray{analyzeTestConn, analyzeMetaConn},
		ApprovalRequiredGroups: pq.StringArray{"sre"},
		ReviewersGroups:        pq.StringArray{"sre"},
		ForceApprovalGroups:    pq.StringArray{},
		SkipReviewGroups:       pq.StringArray{},
	}).Error)
	exec(`INSERT INTO private.ai_session_analyzer_rules (org_id, name, connection_names, risk_evaluation)
		VALUES (?, 'gmud-analyzer', ARRAY[?, ?], ?::jsonb)`, analyzeTestOrgID, analyzeTestConn, analyzeMetaConn,
		defaultRiskEvaluation)
}

// editRule changes the analyzer rule the way PUT /ai/session-analyzer/rules does.
func editRule(t *testing.T, edit func(rule *models.AISessionAnalyzerRules)) {
	t.Helper()
	rule, err := models.GetAISessionAnalyzerRule(uuid.MustParse(analyzeTestOrgID), "gmud-analyzer")
	require.NoError(t, err)
	edit(rule)
	require.NoError(t, models.UpdateAISessionAnalyzerRule(rule))
}

func resetRule(t *testing.T) {
	t.Helper()
	editRule(t, func(rule *models.AISessionAnalyzerRules) {
		require.NoError(t, json.Unmarshal([]byte(defaultRiskEvaluation), &rule.RiskEvaluation))
		rule.CustomPrompt = nil
	})
}

func analyzeTestContext() *storagev2.Context {
	return storagev2.NewContext("user-1", analyzeTestOrgID).
		WithUserInfo("User One", "user1@hoop.dev", "active", "", []string{types.GroupAdmin})
}

func callAnalyze(t *testing.T, script string) *httptest.ResponseRecorder {
	t.Helper()
	return callSessionHandler(t, Analyze, "/api/sessions/analyze", map[string]any{"connection": analyzeTestConn, "script": script})
}

func callPost(t *testing.T, script string) *httptest.ResponseRecorder {
	t.Helper()
	return callSessionHandler(t, Post, "/api/sessions", map[string]any{"connection": analyzeTestConn, "script": script})
}

func callSessionHandler(t *testing.T, handler gin.HandlerFunc, path string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set(storagev2.ContextKey, analyzeTestContext())
	raw, _ := json.Marshal(body)
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(raw)))
	c.Request.Header.Set("Content-Type", "application/json")
	handler(c)
	// The gin engine flushes the status after the handler; a test context does not.
	c.Writer.WriteHeaderNow()
	return rec
}

func setAnalyzeFlag(t *testing.T, on bool) {
	t.Helper()
	featureflag.Set(analyzeTestOrgID, featureflag.FlagAnalyzeBeforeRunForms, on)
	t.Cleanup(func() { featureflag.Set(analyzeTestOrgID, featureflag.FlagAnalyzeBeforeRunForms, false) })
}

func analyzeTestConnection(t *testing.T, name string) *models.Connection {
	t.Helper()
	conn, err := models.GetConnectionByNameOrID(analyzeTestContext(), name)
	require.NoError(t, err)
	require.NotNil(t, conn)
	return conn
}

// isHeld reports whether a verdict is held for the test user, without taking it.
func isHeld(t *testing.T, connection, script string) bool {
	t.Helper()
	key := heldVerdictKey("user-1", analyzeTestConnection(t, connection).ID, script)
	heldVerdicts.mu.Lock()
	defer heldVerdicts.mu.Unlock()
	v, ok := heldVerdicts.entries[analyzeTestOrgID][key]
	return ok && heldVerdicts.now().Before(v.expiresAt)
}

type execResponse struct {
	SessionID    string `json:"session_id"`
	OutputStatus string `json:"output_status"`
	AIAnalysis   struct {
		Action string `json:"action"`
	} `json:"ai_analysis"`
}

func decodeExecResponse(t *testing.T, rec *httptest.ResponseRecorder) execResponse {
	t.Helper()
	var resp execResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	return resp
}

func TestAnalyze(t *testing.T) {
	var calls atomic.Int32
	startAnalyzeTestDB(t, llmStub(t, &calls).URL)

	t.Run("flag off answers 412 and does not analyze", func(t *testing.T) {
		calls.Store(0)
		rec := callAnalyze(t, "SELECT 1")
		assert.Equal(t, http.StatusPreconditionFailed, rec.Code, rec.Body.String())
		assert.Equal(t, int32(0), calls.Load())
	})

	t.Run("a block is final: the blocked session is recorded and returned", func(t *testing.T) {
		setAnalyzeFlag(t, true)
		calls.Store(0)
		rec := callAnalyze(t, "DROP TABLE customers")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, int32(1), calls.Load())

		resp := decodeExecResponse(t, rec)
		assert.Equal(t, "blocked", resp.OutputStatus)
		assert.Equal(t, string(models.BlockExecution), resp.AIAnalysis.Action)

		var status string
		require.NoError(t, models.DB.Raw(`SELECT status FROM private.sessions WHERE id = ?`, resp.SessionID).
			Row().Scan(&status))
		assert.Equal(t, "done", status, "the blocked run is recorded as POST /sessions records it")
		assert.False(t, isHeld(t, analyzeTestConn, "DROP TABLE customers"), "a block is applied, not held")
	})

	t.Run("an allow answers 204 and is held for this user, connection and script", func(t *testing.T) {
		setAnalyzeFlag(t, true)
		calls.Store(0)
		rec := callAnalyze(t, "SELECT * FROM customers")
		require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
		assert.Equal(t, int32(1), calls.Load())

		assert.False(t, isHeld(t, analyzeTestConn, "SELECT * FROM customers -- edited"), "an edited script analyzes again")
		assert.False(t, isHeld(t, analyzeMetaConn, "SELECT * FROM customers"), "another connection analyzes again")
		run, err := takeHeldVerdict(analyzeTestContext(), analyzeTestConnection(t, analyzeTestConn), "SELECT * FROM customers")
		require.NoError(t, err)
		require.NotNil(t, run)
		assert.Equal(t, string(models.AllowExecution), run.analysis.Action)
	})

	t.Run("a verdict serves one run: a concurrent run analyzes again", func(t *testing.T) {
		setAnalyzeFlag(t, true)
		const script = "SELECT 9"
		conn := analyzeTestConnection(t, analyzeTestConn)
		require.Equal(t, http.StatusNoContent, callAnalyze(t, script).Code)

		first, err := takeHeldVerdict(analyzeTestContext(), conn, script)
		require.NoError(t, err)
		require.NotNil(t, first)
		second, err := takeHeldVerdict(analyzeTestContext(), conn, script)
		require.NoError(t, err)
		assert.Nil(t, second, "the first run holds the verdict until it is decided")

		first.release()
		assert.True(t, isHeld(t, analyzeTestConn, script), "a run that failed before it was decided gives it back")
	})

	t.Run("POST /sessions applies the held verdict without a second analysis", func(t *testing.T) {
		setAnalyzeFlag(t, true)
		const script = "UPDATE customers SET tier = 'gold'"
		calls.Store(0)
		require.Equal(t, http.StatusNoContent, callAnalyze(t, script).Code)
		require.Equal(t, int32(1), calls.Load())

		// Medium risk needs approval: POST /sessions answers 202 before it runs anything.
		rec := callPost(t, script)
		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
		assert.Equal(t, int32(1), calls.Load(), "POST /sessions must reuse the held verdict")

		rec = callPost(t, script)
		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
		assert.Equal(t, int32(2), calls.Load(), "a decided run spends the verdict: the next run analyzes again")
	})

	t.Run("a rule changed since the analysis analyzes again", func(t *testing.T) {
		setAnalyzeFlag(t, true)
		t.Cleanup(func() { resetRule(t) })
		const script = "SELECT 7"
		calls.Store(0)
		require.Equal(t, http.StatusNoContent, callAnalyze(t, script).Code)

		// An admin blocks low risk while the user fills in the forms.
		editRule(t, func(rule *models.AISessionAnalyzerRules) {
			rule.RiskEvaluation = models.AISessionAnalyzerRiskEvaluation{
				LowRisk: &models.AISessionAnalyzerRiskTier{Action: models.BlockExecution},
			}
		})
		rec := callPost(t, script)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		resp := decodeExecResponse(t, rec)
		assert.Equal(t, "blocked", resp.OutputStatus)
		assert.Equal(t, string(models.BlockExecution), resp.AIAnalysis.Action)
		assert.Equal(t, int32(2), calls.Load())
	})

	t.Run("a custom prompt added since the analysis analyzes again", func(t *testing.T) {
		setAnalyzeFlag(t, true)
		t.Cleanup(func() { resetRule(t) })
		const script = "UPDATE customers SET tier = 'bronze'"
		calls.Store(0)
		require.Equal(t, http.StatusNoContent, callAnalyze(t, script).Code)
		require.True(t, isHeld(t, analyzeTestConn, script))

		prompt := "Treat every change to customers as high risk."
		editRule(t, func(rule *models.AISessionAnalyzerRules) { rule.CustomPrompt = &prompt })
		require.Equal(t, http.StatusAccepted, callPost(t, script).Code)
		assert.Equal(t, int32(2), calls.Load(), "the held risk level was judged without this prompt")
	})

	t.Run("a 422 before the run keeps the verdict for the retry", func(t *testing.T) {
		setAnalyzeFlag(t, true)
		const script = "SELECT 8"
		body := map[string]any{"connection": analyzeMetaConn, "script": script}
		calls.Store(0)
		require.Equal(t, http.StatusNoContent, callSessionHandler(t, Analyze, "/api/sessions/analyze", body).Code)

		rec := callSessionHandler(t, Post, "/api/sessions", body)
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
		assert.True(t, isHeld(t, analyzeMetaConn, script))
		assert.Equal(t, int32(1), calls.Load())
	})

	t.Run("turning the flag off drops the held verdicts", func(t *testing.T) {
		setAnalyzeFlag(t, true)
		const script = "UPDATE customers SET tier = 'silver'"
		calls.Store(0)
		require.Equal(t, http.StatusNoContent, callAnalyze(t, script).Code)

		featureflag.Set(analyzeTestOrgID, featureflag.FlagAnalyzeBeforeRunForms, false)
		require.Equal(t, http.StatusAccepted, callPost(t, script).Code)
		assert.Equal(t, int32(2), calls.Load(), "flag off: POST /sessions analyzes as before")

		featureflag.Set(analyzeTestOrgID, featureflag.FlagAnalyzeBeforeRunForms, true)
		require.Equal(t, http.StatusAccepted, callPost(t, script).Code)
		assert.Equal(t, int32(3), calls.Load(), "a verdict held before the flag went off is gone")
	})

	t.Run("no analyzer rule answers 204", func(t *testing.T) {
		setAnalyzeFlag(t, true)
		require.NoError(t, models.DB.Exec(`DELETE FROM private.ai_session_analyzer_rules WHERE org_id = ?`, analyzeTestOrgID).Error)
		calls.Store(0)
		rec := callAnalyze(t, "DROP TABLE customers")
		assert.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
		assert.Equal(t, int32(0), calls.Load())
	})
}

func TestVerdictHold(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	h := &verdictHold{entries: map[string]map[string]heldVerdict{}, now: func() time.Time { return now }}
	allow := &models.SessionAIAnalysis{Action: string(models.AllowExecution)}

	h.put("org1", "a", heldVerdict{analysis: allow, expiresAt: now.Add(heldVerdictTTL)})
	h.put("org1", "old", heldVerdict{analysis: allow, expiresAt: now.Add(time.Minute)})
	h.put("org2", "old", heldVerdict{analysis: allow, expiresAt: now.Add(time.Minute)})

	now = now.Add(2 * time.Minute)
	h.put("org1", "b", heldVerdict{analysis: allow, expiresAt: now.Add(heldVerdictTTL)})
	assert.NotContains(t, h.entries["org1"], "old", "a put drops expired entries")
	assert.NotContains(t, h.entries, "org2", "a put drops orgs with no verdict left")

	v, ok := h.take("org1", "a")
	require.True(t, ok)
	assert.Same(t, allow, v.analysis)
	_, ok = h.take("org1", "a")
	assert.False(t, ok, "take is one use")
	h.restore("org1", "a", v)
	_, ok = h.take("org2", "a")
	assert.False(t, ok, "verdicts are per org")
	_, ok = h.take("org1", "a")
	assert.True(t, ok, "restore gives the verdict back")

	now = now.Add(heldVerdictTTL)
	_, ok = h.take("org1", "b")
	assert.False(t, ok, "an expired verdict is not returned")
	assert.Empty(t, h.entries, "take drops the expired entry and the empty org")

	h.restore("org1", "b", heldVerdict{analysis: allow, expiresAt: now})
	assert.Empty(t, h.entries, "an expired verdict is not restored")
}

func TestVerdictHoldSweepsAtMostEachMinute(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	h := &verdictHold{entries: map[string]map[string]heldVerdict{}, now: func() time.Time { return now }}
	allow := &models.SessionAIAnalysis{Action: string(models.AllowExecution)}

	h.put("org1", "short", heldVerdict{analysis: allow, expiresAt: now.Add(time.Second)})
	now = now.Add(2 * time.Second)
	h.put("org1", "next", heldVerdict{analysis: allow, expiresAt: now.Add(heldVerdictTTL)})
	assert.Contains(t, h.entries["org1"], "short", "no sweep within a minute of the last one")

	now = now.Add(heldVerdictSweep)
	h.put("org1", "later", heldVerdict{analysis: allow, expiresAt: now.Add(heldVerdictTTL)})
	assert.NotContains(t, h.entries["org1"], "short")
}

func TestVerdictHoldKeepsTheStricterVerdict(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	h := &verdictHold{entries: map[string]map[string]heldVerdict{}, now: func() time.Time { return now }}
	ruleID := uuid.New()
	verdict := func(action models.RiskEvaluationAction, updatedAt time.Time) heldVerdict {
		return heldVerdict{
			analysis:      &models.SessionAIAnalysis{Action: string(action)},
			ruleID:        ruleID,
			ruleUpdatedAt: updatedAt,
			expiresAt:     now.Add(heldVerdictTTL),
		}
	}
	held := func() string {
		v, ok := h.take("org1", "k")
		require.True(t, ok)
		h.restore("org1", "k", v)
		return v.analysis.Action
	}

	h.put("org1", "k", verdict(models.RequireAccessRequest, now))
	h.put("org1", "k", verdict(models.AllowExecution, now))
	assert.Equal(t, string(models.RequireAccessRequest), held(), "a retry that gets allow keeps the approval verdict")

	h.put("org1", "k", verdict(models.AllowExecution, now.Add(time.Second)))
	assert.Equal(t, string(models.AllowExecution), held(), "a new rule version replaces the verdict")

	h.put("org1", "k", verdict(models.RequireAccessRequest, now.Add(time.Second)))
	assert.Equal(t, string(models.RequireAccessRequest), held(), "a stricter verdict replaces a permissive one")
}

func TestHeldVerdictKey(t *testing.T) {
	base := heldVerdictKey("user", "conn", "SELECT 1")
	assert.Equal(t, base, heldVerdictKey("user", "conn", "SELECT 1"))
	for name, key := range map[string]string{
		"other user":       heldVerdictKey("user2", "conn", "SELECT 1"),
		"other connection": heldVerdictKey("user", "conn2", "SELECT 1"),
		"other script":     heldVerdictKey("user", "conn", "SELECT 2"),
		"shifted parts":    heldVerdictKey("use", "rconn", "SELECT 1"),
	} {
		assert.NotEqual(t, base, key, name)
	}
}
