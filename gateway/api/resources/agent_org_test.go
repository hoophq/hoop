package resources

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
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) { os.Exit(pglitetest.Main(m)) }

const (
	resOrgA = "00000000-0000-0000-0000-00000000113a"
	resOrgB = "00000000-0000-0000-0000-00000000113b"
)

func callResourceAsOrgA(handler gin.HandlerFunc, method, target, body string, params ...gin.Param) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set(storagev2.ContextKey, storagev2.NewContext("user-a", resOrgA).
		WithUserInfo("Admin", "admin@a.test", "active", "", []string{types.GroupAdmin}))
	c.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = params
	handler(c)
	c.Writer.WriteHeaderNow()
	return rec
}

// A resource or a resource role with an agent of another org is refused and
// nothing is written. An unknown agent passes, as before.
func TestResourceRefusesAgentOfAnotherOrg(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	inst := pglitetest.StartMigrated(t)
	require.NoError(t, models.InitDatabaseConnection(inst.DSN(), 1))
	sqlDB, err := models.DB.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	for _, orgID := range []string{resOrgA, resOrgB} {
		require.NoError(t, models.DB.Exec(`INSERT INTO private.orgs (id, name) VALUES (?, ?)`, orgID, "org-"+orgID).Error)
	}
	require.NoError(t, models.CreateAgent(resOrgB, "_default", "standard", "hash-b"))
	agentB, err := models.GetAgentByNameOrID(resOrgB, "_default")
	require.NoError(t, err)
	require.NoError(t, models.DB.Exec(`INSERT INTO private.resources (org_id, name, type, subtype)
		VALUES (?, 'res-a', 'custom', 'custom')`, resOrgA).Error)

	for _, agentID := range []string{agentB.ID} {
		bodies := []string{
			fmt.Sprintf(`{"name":"res-x","type":"custom","env_vars":{},"agent_id":%q}`, agentID),
			fmt.Sprintf(`{"name":"res-x","type":"custom","env_vars":{},
				"roles":[{"name":"role-x","type":"custom","command":["bash"],"agent_id":%q}]}`, agentID),
		}
		for _, body := range bodies {
			rec := callResourceAsOrgA(CreateResource, http.MethodPost, "/api/resources", body)
			assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			assert.JSONEq(t, `{"message":"agent not found"}`, rec.Body.String())
		}

		rec := callResourceAsOrgA(UpdateResource, http.MethodPut, "/api/resources/res-a",
			fmt.Sprintf(`{"name":"res-a","type":"custom","subtype":"custom","env_vars":{},"agent_id":%q}`, agentID),
			gin.Param{Key: "name", Value: "res-a"})
		assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		assert.JSONEq(t, `{"message":"agent not found"}`, rec.Body.String())
	}

	var resources, connections int64
	require.NoError(t, models.DB.Raw(`SELECT count(*) FROM private.resources WHERE org_id = ?`, resOrgA).Scan(&resources).Error)
	require.NoError(t, models.DB.Raw(`SELECT count(*) FROM private.connections WHERE org_id = ?`, resOrgA).Scan(&connections).Error)
	assert.EqualValues(t, 1, resources)
	assert.Zero(t, connections)
	var agentID *string
	require.NoError(t, models.DB.Raw(`SELECT agent_id::TEXT FROM private.resources WHERE org_id = ? AND name = 'res-a'`,
		resOrgA).Scan(&agentID).Error)
	assert.Nil(t, agentID)

	const unknownAgent = "00000000-0000-0000-0000-0000000011ff"
	rec := callResourceAsOrgA(UpdateResource, http.MethodPut, "/api/resources/res-a",
		fmt.Sprintf(`{"name":"res-a","type":"custom","subtype":"custom","env_vars":{},"agent_id":%q}`, unknownAgent),
		gin.Param{Key: "name", Value: "res-a"})
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}
