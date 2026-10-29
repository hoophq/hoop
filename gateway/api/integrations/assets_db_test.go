package apijiraintegration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite/pglitetest"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) { os.Exit(pglitetest.Main(m)) }

const assetsTestOrgID = "00000000-0000-0000-0000-0000000000f7"

func startAssetsTestDB(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	inst := pglitetest.StartMigrated(t)
	// The embedded backend serves one session at a time.
	require.NoError(t, models.InitDatabaseConnection(inst.DSN(), 1))
	sqlDB, err := models.DB.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, models.DB.Exec(
		`INSERT INTO private.orgs (id, name) VALUES (?, 'jira-assets-test')`, assetsTestOrgID).Error)
}

func callAssets(handler gin.HandlerFunc, target string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set(storagev2.ContextKey, storagev2.NewContext("admin@hoop.dev", assetsTestOrgID).
		WithUserInfo("Admin", "admin@hoop.dev", "active", "", []string{types.GroupAdmin}))
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	handler(c)
	return rec
}

func TestAssetRoutesWithoutJiraIntegration(t *testing.T) {
	startAssetsTestDB(t)
	for _, tt := range []struct {
		msg     string
		handler gin.HandlerFunc
		target  string
	}{
		{"objects", GetAssetObjects, "/integrations/jira/assets/objects?object_type_id=1&limit=10"},
		{"fieldconfigs", GetAssetFieldConfigs, "/integrations/jira/assets/fieldconfigs?jira_fields=customfield_10092"},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			rec := callAssets(tt.handler, tt.target)
			assert.Equal(t, http.StatusNotFound, rec.Code)
			assert.JSONEq(t, `{"message":"Jira integration not found"}`, rec.Body.String())
		})
	}
}

func TestAssetFieldConfigsWithJiraIntegration(t *testing.T) {
	startAssetsTestDB(t)
	jiraSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/api/2/field/customfield_10092/context":
			_, _ = w.Write([]byte(`{"values":[{"id":"10200","isGlobalContext":true}]}`))
		case "/rest/servicedesk/cmdb/latest/fieldconfig/10200":
			_, _ = w.Write([]byte(`{"objectSchemaId":"9","objectFilterQuery":"objectType = \"Service\""}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(jiraSrv.Close)
	_, err := models.CreateJiraIntegration(assetsTestOrgID, &models.JiraIntegration{
		ID:     uuid.NewString(),
		OrgID:  assetsTestOrgID,
		URL:    jiraSrv.URL,
		User:   "admin@hoop.dev",
		Status: models.JiraIntegrationStatusActive,
	})
	require.NoError(t, err)

	rec := callAssets(GetAssetFieldConfigs, "/integrations/jira/assets/fieldconfigs?jira_fields=customfield_10092")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var got openapi.JiraAssetFieldConfigs
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, []openapi.JiraAssetFieldConfig{{
		JiraField:         "customfield_10092",
		ObjectSchemaID:    "9",
		ObjectFilterQuery: `objectType = "Service"`,
	}}, got.Items)
}
