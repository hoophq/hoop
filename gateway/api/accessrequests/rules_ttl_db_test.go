package accessrequests

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	modelsbootstrap "github.com/hoophq/hoop/gateway/models/bootstrap"
	"github.com/hoophq/hoop/gateway/pglite"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const ttlTestOrgID = "00000000-0000-0000-0000-0000000000d1"

func startRulesTestDB(t *testing.T) {
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
		`INSERT INTO private.orgs (id, name) VALUES (?, 'rules-ttl-test')`, ttlTestOrgID).Error)
}

// callRule runs a rules handler as an admin of an enterprise org, so the OSS
// limit of one rule does not apply.
func callRule(t *testing.T, handler gin.HandlerFunc, method, name, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set(storagev2.ContextKey, storagev2.NewContext("admin-1", ttlTestOrgID).
		WithUserInfo("Admin", "admin@hoop.dev", "active", "", []string{"admin"}).
		WithOrgLicenseData(json.RawMessage(`{"payload":{"type":"enterprise"}}`)))
	c.Params = gin.Params{{Key: "name", Value: name}}
	c.Request = httptest.NewRequest(method, "/api/access-requests/rules/"+name, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	handler(c)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

// storedTTLs reads the columns, so a stored 0 and a NULL differ.
func storedTTLs(t *testing.T, name string) (pending, approval sql.NullInt64) {
	t.Helper()
	row := models.DB.Raw(`SELECT pending_ttl_sec, approval_ttl_sec FROM private.access_request_rules
		WHERE org_id = ? AND name = ?`, ttlTestOrgID, name).Row()
	require.NoError(t, row.Scan(&pending, &approval))
	return pending, approval
}

func assertTTLs(t *testing.T, step, name string, pending, approval int64) {
	t.Helper()
	p, a := storedTTLs(t, name)
	want := func(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: v != 0} }
	assert.Equal(t, want(pending), p, "%s: pending_ttl_sec (0 means NULL)", step)
	assert.Equal(t, want(approval), a, "%s: approval_ttl_sec (0 means NULL)", step)
}

// A sidecar rule's limits: create reads absent or 0 as no limit; update reads
// absent as keep and 0 as clear, so a client that does not know them keeps them.
func TestSidecarRuleHandlersStoreTheTTLs(t *testing.T) {
	startRulesTestDB(t)
	// The managed path stores the group lists as sent, so the body names them all.
	const base = `"access_type":"sidecar","approval_required_groups":[],"reviewers_groups":["sre"],` +
		`"force_approval_groups":[],"min_approvals":1`

	rec, out := callRule(t, CreateAccessRequestRule, http.MethodPost, "",
		`{"name":"limited",`+base+`,"pending_ttl_sec":900,"approval_ttl_sec":0}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, float64(900), out["pending_ttl_sec"])
	assert.NotContains(t, out, "approval_ttl_sec", "0 on create is no limit")
	assertTTLs(t, "create", "limited", 900, 0)

	rec, _ = callRule(t, CreateAccessRequestRule, http.MethodPost, "", `{"name":"unlimited",`+base+`}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assertTTLs(t, "create without the keys", "unlimited", 0, 0)

	rec, out = callRule(t, UpdateAccessRequestRule, http.MethodPut, "limited",
		`{"name":"limited","description":"edited",`+base+`}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, float64(900), out["pending_ttl_sec"], "the response shows the kept limit")
	assertTTLs(t, "update without the keys keeps", "limited", 900, 0)

	rec, _ = callRule(t, UpdateAccessRequestRule, http.MethodPut, "limited",
		`{"name":"limited",`+base+`,"approval_ttl_sec":600}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assertTTLs(t, "update of one limit keeps the other", "limited", 900, 600)

	rec, out = callRule(t, UpdateAccessRequestRule, http.MethodPut, "limited",
		`{"name":"limited",`+base+`,"pending_ttl_sec":0}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotContains(t, out, "pending_ttl_sec")
	assertTTLs(t, "0 clears", "limited", 0, 600)

	rec, _ = callRule(t, UpdateAccessRequestRule, http.MethodPut, "limited",
		`{"name":"limited","description":"refused",`+base+`,"pending_ttl_sec":30}`)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "pending_ttl_sec")
	assertTTLs(t, "a refused update writes nothing", "limited", 0, 600)

	rec, out = callRule(t, GetAccessRequestRule, http.MethodGet, "limited", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, float64(600), out["approval_ttl_sec"])
	assert.NotEqual(t, "refused", out["description"], "the refused update wrote the description")

	// A managed rule is edited through its own path, which keeps what the
	// analyzer rule set.
	orgID := uuid.MustParse(ttlTestOrgID)
	require.NoError(t, services.SyncAnalyzerApprovalRule(models.DB, orgID, "hold-writes",
		json.RawMessage(`{"high":"require_review","approval_rule":"hold-writes"}`), nil))
	pending, approval := 900, 600
	require.NoError(t, services.ApplyAnalyzerApprovalTTLs(models.DB, orgID, "hold-writes", &pending, &approval))
	rec, out = callRule(t, UpdateAccessRequestRule, http.MethodPut, "hold-writes",
		`{"name":"hold-writes",`+base+`,"pending_ttl_sec":60,"approval_ttl_sec":0}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, float64(900), out["pending_ttl_sec"])
	assert.Equal(t, float64(600), out["approval_ttl_sec"])
	assertTTLs(t, "a managed rule keeps its limits", "hold-writes", 900, 600)
}
