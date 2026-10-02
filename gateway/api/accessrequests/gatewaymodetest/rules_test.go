package gatewaymodetest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/accessrequests"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	modelsbootstrap "github.com/hoophq/hoop/gateway/models/bootstrap"
	"github.com/hoophq/hoop/gateway/pglite"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const orgID = "00000000-0000-0000-0000-0000000000d2"

func TestMain(m *testing.M) {
	if err := appconfig.Load(appconfig.AppModeGateway); err != nil {
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
		`INSERT INTO private.orgs (id, name) VALUES (?, 'rules-gateway-test')`, orgID).Error)
}

func callRule(t *testing.T, handler gin.HandlerFunc, method, name, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set(storagev2.ContextKey, storagev2.NewContext("admin-1", orgID).
		WithUserInfo("Admin", "admin@hoop.dev", "active", "", []string{"admin"}).
		WithOrgLicenseData(json.RawMessage(`{"payload":{"type":"enterprise"}}`)))
	c.Params = gin.Params{{Key: "name", Value: name}}
	c.Request = httptest.NewRequest(method, "/api/access-requests/rules/"+name, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	handler(c)
	return rec
}

// A gateway rule has no review limit: the keys are accepted, not stored, not
// checked and not echoed, so gateway answers stay as they were.
func TestAGatewayRuleIgnoresTheTTLs(t *testing.T) {
	startDB(t)
	const rule = `"access_type":"jit","connection_names":["pg-prod"],"approval_required_groups":[],` +
		`"reviewers_groups":["sre"],"force_approval_groups":[],"min_approvals":1`

	for _, step := range []struct {
		name    string
		handler gin.HandlerFunc
		method  string
		want    int
		body    string
	}{
		{"create", accessrequests.CreateAccessRequestRule, http.MethodPost, http.StatusCreated,
			`{"name":"jit-rule",` + rule + `,"pending_ttl_sec":900,"approval_ttl_sec":600}`},
		// Out of bounds too: a gateway never reads the keys, so it never refuses them.
		{"update", accessrequests.UpdateAccessRequestRule, http.MethodPut, http.StatusOK,
			`{"name":"jit-rule",` + rule + `,"pending_ttl_sec":30,"approval_ttl_sec":604801}`},
	} {
		rec := callRule(t, step.handler, step.method, "jit-rule", step.body)
		require.Equal(t, step.want, rec.Code, "%s: %s", step.name, rec.Body)
		assert.NotContains(t, rec.Body.String(), "ttl_sec", step.name)

		var nulls bool
		require.NoError(t, models.DB.Raw(`SELECT pending_ttl_sec IS NULL AND approval_ttl_sec IS NULL
			FROM private.access_request_rules WHERE org_id = ? AND name = 'jit-rule'`, orgID).Scan(&nulls).Error)
		assert.True(t, nulls, "%s: a gateway rule stored a limit", step.name)
	}
}
