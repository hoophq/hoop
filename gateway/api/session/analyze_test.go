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
)

// llmStub answers as an OpenAI-compatible provider: a script with DROP is high
// risk, UPDATE medium, anything else low. calls counts the analyzer runs.
func llmStub(t *testing.T, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		tool, title := "LowRiskAISessionAnalyzer", "Read only"
		switch {
		case strings.Contains(string(body), "DROP"):
			tool, title = "HighRiskAISessionAnalyzer", "Drops a table"
		case strings.Contains(string(body), "UPDATE"):
			tool, title = "MediumRiskAISessionAnalyzer", "Changes rows"
		}
		args, _ := json.Marshal(map[string]string{"title": title, "explanation": title + "."})
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"c1","object":"chat.completion","created":0,"model":"stub",
			"choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","content":"",
			"tool_calls":[{"id":"t1","type":"function","function":{"name":%q,"arguments":%q}}]}}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`, tool, string(args))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// startAnalyzeTestDB seeds an org, a connection, an AI provider that points at
// the stub, and an analyzer rule: medium risk needs approval, high risk blocks.
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
	exec(`INSERT INTO private.resources (org_id, name, type, subtype) VALUES (?, ?, 'database', 'postgres')`,
		analyzeTestOrgID, analyzeTestConn)
	exec(`INSERT INTO private.connections (org_id, name, type, subtype, resource_name, access_mode_exec)
		VALUES (?, ?, 'database', 'postgres', ?, 'enabled'::private.enum_access_status)`,
		analyzeTestOrgID, analyzeTestConn, analyzeTestConn)
	exec(`INSERT INTO private.ai_providers (org_id, feature, provider, api_url, api_key, model)
		VALUES (?, 'session-analyzer', 'openai', ?, 'stub-key', 'stub')`, analyzeTestOrgID, llmURL+"/v1/")
	require.NoError(t, models.DB.Create(&models.AccessRequestRule{
		OrgID:                  uuid.MustParse(analyzeTestOrgID),
		Name:                   "gmud-approval",
		AccessType:             models.AccessTypeCommand,
		ConnectionNames:        pq.StringArray{analyzeTestConn},
		ApprovalRequiredGroups: pq.StringArray{"sre"},
		ReviewersGroups:        pq.StringArray{"sre"},
		ForceApprovalGroups:    pq.StringArray{},
		SkipReviewGroups:       pq.StringArray{},
	}).Error)
	exec(`INSERT INTO private.ai_session_analyzer_rules (org_id, name, connection_names, risk_evaluation)
		VALUES (?, 'block-high', ARRAY[?], ?::jsonb)`, analyzeTestOrgID, analyzeTestConn,
		`{"low_risk":{"action":"allow_execution"},
		"medium_risk":{"action":"require_access_request","access_request_rule_name":"gmud-approval"},
		"high_risk":{"action":"block_execution"}}`)
}

func analyzeTestContext() *storagev2.Context {
	return storagev2.NewContext("user-1", analyzeTestOrgID).
		WithUserInfo("User One", "user1@hoop.dev", "active", "", []string{types.GroupAdmin})
}

func callAnalyze(t *testing.T, script string) *httptest.ResponseRecorder {
	t.Helper()
	return callSessionHandler(t, Analyze, "/api/sessions/analyze", script)
}

func callSessionHandler(t *testing.T, handler gin.HandlerFunc, path, script string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set(storagev2.ContextKey, analyzeTestContext())
	body, _ := json.Marshal(map[string]any{"connection": analyzeTestConn, "script": script})
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(body)))
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

func analyzeTestConnection(t *testing.T) *models.Connection {
	t.Helper()
	conn, err := models.GetConnectionByNameOrID(analyzeTestContext(), analyzeTestConn)
	require.NoError(t, err)
	require.NotNil(t, conn)
	return conn
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

		var resp struct {
			SessionID    string `json:"session_id"`
			OutputStatus string `json:"output_status"`
			AIAnalysis   struct {
				Action string `json:"action"`
			} `json:"ai_analysis"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.Equal(t, "blocked", resp.OutputStatus)
		assert.Equal(t, string(models.BlockExecution), resp.AIAnalysis.Action)

		var status string
		require.NoError(t, models.DB.Raw(`SELECT status FROM private.sessions WHERE id = ?`, resp.SessionID).
			Row().Scan(&status))
		assert.Equal(t, "done", status, "the blocked run is recorded as POST /sessions records it")

		_, _, held := takeHeldVerdict(analyzeTestContext(), analyzeTestConnection(t), "DROP TABLE customers")
		assert.False(t, held, "a block must not be held: it was already applied")
	})

	t.Run("an allow answers 204 and is held once for POST /sessions", func(t *testing.T) {
		setAnalyzeFlag(t, true)
		calls.Store(0)
		rec := callAnalyze(t, "SELECT * FROM customers")
		require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
		assert.Equal(t, int32(1), calls.Load())

		conn := analyzeTestConnection(t)
		_, _, held := takeHeldVerdict(analyzeTestContext(), conn, "SELECT * FROM customers -- edited")
		assert.False(t, held, "an edited script must be analyzed again")

		analysis, _, held := takeHeldVerdict(analyzeTestContext(), conn, "SELECT * FROM customers")
		require.True(t, held)
		assert.Equal(t, string(models.AllowExecution), analysis.Action)

		_, _, held = takeHeldVerdict(analyzeTestContext(), conn, "SELECT * FROM customers")
		assert.False(t, held, "a held verdict is used once")
	})

	t.Run("POST /sessions applies the held verdict without a second analysis", func(t *testing.T) {
		setAnalyzeFlag(t, true)
		const script = "UPDATE customers SET tier = 'gold'"
		calls.Store(0)
		require.Equal(t, http.StatusNoContent, callAnalyze(t, script).Code)
		require.Equal(t, int32(1), calls.Load())

		// Medium risk needs approval: POST /sessions answers 202 before it runs anything.
		rec := callSessionHandler(t, Post, "/api/sessions", script)
		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
		assert.Equal(t, int32(1), calls.Load(), "POST /sessions must reuse the held verdict")

		rec = callSessionHandler(t, Post, "/api/sessions", script)
		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
		assert.Equal(t, int32(2), calls.Load(), "a used verdict is gone: the next run analyzes again")
	})

	t.Run("flag off: POST /sessions does not read held verdicts", func(t *testing.T) {
		setAnalyzeFlag(t, true)
		require.Equal(t, http.StatusNoContent, callAnalyze(t, "SELECT 2").Code)
		featureflag.Set(analyzeTestOrgID, featureflag.FlagAnalyzeBeforeRunForms, false)
		_, _, held := takeHeldVerdict(analyzeTestContext(), analyzeTestConnection(t), "SELECT 2")
		assert.False(t, held)
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
	h := &verdictHold{entries: map[string]heldVerdict{}, now: func() time.Time { return now }}
	allow := &models.SessionAIAnalysis{Action: string(models.AllowExecution)}

	h.put("a", heldVerdict{analysis: allow, expiresAt: now.Add(heldVerdictTTL)})
	h.put("old", heldVerdict{analysis: allow, expiresAt: now.Add(time.Minute)})

	now = now.Add(2 * time.Minute)
	h.put("b", heldVerdict{analysis: allow, expiresAt: now.Add(heldVerdictTTL)})
	assert.NotContains(t, h.entries, "old", "put drops expired entries")

	v, ok := h.take("a")
	require.True(t, ok)
	assert.Same(t, allow, v.analysis)
	_, ok = h.take("a")
	assert.False(t, ok, "take is one use")

	now = now.Add(heldVerdictTTL)
	_, ok = h.take("b")
	assert.False(t, ok, "an expired verdict is not returned")
	assert.Empty(t, h.entries)
}

func TestHeldVerdictKey(t *testing.T) {
	base := heldVerdictKey("org", "user", "conn", "SELECT 1")
	assert.Equal(t, base, heldVerdictKey("org", "user", "conn", "SELECT 1"))
	for name, key := range map[string]string{
		"other org":        heldVerdictKey("org2", "user", "conn", "SELECT 1"),
		"other user":       heldVerdictKey("org", "user2", "conn", "SELECT 1"),
		"other connection": heldVerdictKey("org", "user", "conn2", "SELECT 1"),
		"other script":     heldVerdictKey("org", "user", "conn", "SELECT 2"),
		"shifted parts":    heldVerdictKey("org", "use", "rconn", "SELECT 1"),
	} {
		assert.NotEqual(t, base, key, name)
	}
}
