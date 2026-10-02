package controlplanetest

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	apiai "github.com/hoophq/hoop/gateway/api/ai"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	modelsbootstrap "github.com/hoophq/hoop/gateway/models/bootstrap"
	"github.com/hoophq/hoop/gateway/pglite"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const orgID = "00000000-0000-0000-0000-0000000000d3"

func TestMain(m *testing.M) {
	if err := appconfig.Load(appconfig.AppModeControlPlane); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

func startDB(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	ctx := context.Background()
	inst, err := pglite.Start(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := inst.Close(ctx); err != nil {
			t.Errorf("close embedded database: %v", err)
		}
	})
	require.NoError(t, modelsbootstrap.MigrateDB(inst.MigrateDSN(), ""))
	// The embedded backend serves one session at a time.
	require.NoError(t, models.InitDatabaseConnection(inst.DSN(), 1))
	require.NoError(t, models.DB.Exec(
		`INSERT INTO private.orgs (id, name) VALUES (?, 'analyzer-ttl-test')`, orgID).Error)
}

func callRule(t *testing.T, handler gin.HandlerFunc, method, name, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set(storagev2.ContextKey, storagev2.NewContext("admin-1", orgID).
		WithUserInfo("Admin", "admin@hoop.dev", "active", "", []string{"admin"}))
	c.Params = gin.Params{{Key: "name", Value: name}}
	c.Request = httptest.NewRequest(method, "/api/ai/session-analyzer/rules/"+name, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	handler(c)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

// storedTTLs reads the approval rule's columns; 0 stands for NULL.
func storedTTLs(t *testing.T, name string) (pending, approval int64) {
	t.Helper()
	var p, a sql.NullInt64
	row := models.DB.Raw(`SELECT pending_ttl_sec, approval_ttl_sec FROM private.access_request_rules
		WHERE org_id = ? AND name = ?`, orgID, name).Row()
	require.NoError(t, row.Scan(&p, &a))
	return p.Int64, a.Int64
}

// The analyzer routes write the approval rule's limits in the save's transaction,
// keep them when absent, and answer 422 with nothing written when out of bounds.
func TestTheAnalyzerRoutesCarryTheHoldTTLs(t *testing.T) {
	startDB(t)
	rule := func(extra string) string {
		return `{"name":"hold-writes","connection_names":[],"agentic":false,` +
			`"risk_evaluation":{"low_risk_action":"allow_execution","medium_risk_action":"allow_execution","high_risk_action":"allow_execution"},` +
			`"sidecar_spec":{"high":"require_review","approval_rule":"hold-writes"}` + extra + `}`
	}

	rec, out := callRule(t, apiai.CreateSessionAnalyzerRule, http.MethodPost, "", rule(`,"pending_ttl_sec":900`))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, float64(900), out["pending_ttl_sec"])
	assert.NotContains(t, out, "approval_ttl_sec")
	assert.Equal(t, []any{"admin"}, out["reviewers_groups"], "the reviewers still read back")
	p, a := storedTTLs(t, "hold-writes")
	assert.Equal(t, [2]int64{900, 0}, [2]int64{p, a}, "create")

	rec, out = callRule(t, apiai.GetSessionAnalyzerRule, http.MethodGet, "hold-writes", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, float64(900), out["pending_ttl_sec"], "the edit form loads the limits")

	rec, out = callRule(t, apiai.UpdateSessionAnalyzerRule, http.MethodPut, "hold-writes",
		rule(`,"description":"edited"`))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, float64(900), out["pending_ttl_sec"])
	p, a = storedTTLs(t, "hold-writes")
	assert.Equal(t, [2]int64{900, 0}, [2]int64{p, a}, "an edit without the limits keeps them")

	rec, out = callRule(t, apiai.UpdateSessionAnalyzerRule, http.MethodPut, "hold-writes",
		rule(`,"description":"edited","pending_ttl_sec":0,"approval_ttl_sec":600`))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotContains(t, out, "pending_ttl_sec")
	assert.Equal(t, float64(600), out["approval_ttl_sec"])
	p, a = storedTTLs(t, "hold-writes")
	assert.Equal(t, [2]int64{0, 600}, [2]int64{p, a}, "0 clears, a value sets")

	rec, _ = callRule(t, apiai.UpdateSessionAnalyzerRule, http.MethodPut, "hold-writes",
		rule(`,"description":"refused","pending_ttl_sec":30`))
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "pending_ttl_sec")
	p, a = storedTTLs(t, "hold-writes")
	assert.Equal(t, [2]int64{0, 600}, [2]int64{p, a}, "a refused edit writes no limit")
	rec, out = callRule(t, apiai.GetSessionAnalyzerRule, http.MethodGet, "hold-writes", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "edited", out["description"], "a refused edit rolls back the analyzer rule too")

	rec, _ = callRule(t, apiai.CreateSessionAnalyzerRule, http.MethodPost, "",
		strings.Replace(rule(`,"approval_ttl_sec":604801`), "hold-writes", "hold-more", 2))
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "approval_ttl_sec")
	rec, _ = callRule(t, apiai.GetSessionAnalyzerRule, http.MethodGet, "hold-more", "")
	assert.Equal(t, http.StatusNotFound, rec.Code, "a refused create leaves no rule")
}
