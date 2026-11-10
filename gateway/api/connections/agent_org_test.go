package apiconnections

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite/pglitetest"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) { os.Exit(pglitetest.Main(m)) }

const (
	agentOrgA = "00000000-0000-0000-0000-00000000112a"
	agentOrgB = "00000000-0000-0000-0000-00000000112b"
)

// startAgentOrgDB returns the id of the agent of org A and of org B.
func startAgentOrgDB(t *testing.T) (string, string) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	inst := pglitetest.StartMigrated(t)
	require.NoError(t, models.InitDatabaseConnection(inst.DSN(), 1))
	sqlDB, err := models.DB.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	var ids []string
	for _, orgID := range []string{agentOrgA, agentOrgB} {
		require.NoError(t, models.DB.Exec(`INSERT INTO private.orgs (id, name) VALUES (?, ?)`, orgID, "org-"+orgID).Error)
		for _, name := range []string{plugintypes.PluginAuditName, plugintypes.PluginEditorName,
			plugintypes.PluginSlackName, plugintypes.PluginDLPName, plugintypes.PluginReviewName} {
			require.NoError(t, models.DB.Exec(`INSERT INTO private.plugins (org_id, name) VALUES (?, ?)
				ON CONFLICT DO NOTHING`, orgID, name).Error)
		}
		require.NoError(t, models.CreateAgent(orgID, "_default", "standard", "hash-"+orgID))
		agent, err := models.GetAgentByNameOrID(orgID, "_default")
		require.NoError(t, err)
		ids = append(ids, agent.ID)
	}
	return ids[0], ids[1]
}

func callAsOrgA(handler gin.HandlerFunc, method, target, body string, params ...gin.Param) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set(storagev2.ContextKey, storagev2.NewContext("user-a", agentOrgA).
		WithUserInfo("Admin", "admin@a.test", "active", "", []string{types.GroupAdmin}))
	c.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = params
	handler(c)
	c.Writer.WriteHeaderNow()
	return rec
}

func connectionBody(name, agentID string) string {
	return fmt.Sprintf(`{"name":%q,"type":"custom","subtype":"custom","agent_id":%q,"command":["bash"],
		"access_mode_runbooks":"enabled","access_mode_exec":"enabled","access_mode_connect":"enabled","access_schema":"disabled"}`,
		name, agentID)
}

func connectionAgentID(t *testing.T, name string) string {
	t.Helper()
	var agentID string
	require.NoError(t, models.DB.Raw(`SELECT COALESCE(agent_id::TEXT, '') FROM private.connections
		WHERE org_id = ? AND name = ?`, agentOrgA, name).Scan(&agentID).Error)
	return agentID
}

// An agent of another org is refused and nothing is written. An unknown
// agent passes, as before: a single-org gateway can hold one after the agent
// is deleted.
func TestConnectionRefusesAgentOfAnotherOrg(t *testing.T) {
	agentA, agentB := startAgentOrgDB(t)
	const unknownAgent = "00000000-0000-0000-0000-0000000011ff"
	nameParam := gin.Param{Key: "nameOrID", Value: "conn-a"}

	rec := callAsOrgA(Post, http.MethodPost, "/api/connections", connectionBody("conn-a", agentA))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	rec = callAsOrgA(Post, http.MethodPost, "/api/connections", connectionBody("conn-u", unknownAgent))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	rec = callAsOrgA(Put, http.MethodPut, "/api/connections/conn-u", connectionBody("conn-u", unknownAgent),
		gin.Param{Key: "nameOrID", Value: "conn-u"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	{
		agentID := agentB
		rec := callAsOrgA(Post, http.MethodPost, "/api/connections", connectionBody("conn-x", agentID))
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
		assert.JSONEq(t, `{"message":"agent not found"}`, rec.Body.String())

		rec = callAsOrgA(Put, http.MethodPut, "/api/connections/conn-a", connectionBody("conn-a", agentID), nameParam)
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
		assert.JSONEq(t, `{"message":"agent not found"}`, rec.Body.String())

		rec = callAsOrgA(Patch, http.MethodPatch, "/api/connections/conn-a",
			fmt.Sprintf(`{"agent_id":%q}`, agentID), nameParam)
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
		assert.JSONEq(t, `{"message":"agent not found"}`, rec.Body.String())

		rec = callAsOrgA(TestFederationConfig, http.MethodPost, "/api/federation/test", fmt.Sprintf(`{
			"user_email":"alice@a.test",
			"config":{"hook_source":"builtin","builtin_provider":"gcp_iam",
				"identity_source_attribute":"$.user.email","identity_target_template":"{user.email}","fallback_policy":"deny"},
			"connection":{"agent_id":%q,"command":["bash"],"test_script":"id"}}`, agentID))
		assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		assert.JSONEq(t, `{"message":"agent not found"}`, rec.Body.String())
	}

	var count int64
	require.NoError(t, models.DB.Raw(`SELECT count(*) FROM private.connections WHERE org_id = ?`, agentOrgA).Scan(&count).Error)
	assert.EqualValues(t, 2, count)
	assert.Equal(t, agentA, connectionAgentID(t, "conn-a"))

	rec = callAsOrgA(Patch, http.MethodPatch, "/api/connections/conn-a", `{"agent_id":""}`, nameParam)
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Empty(t, connectionAgentID(t, "conn-a"), "patch still clears the agent")
}
