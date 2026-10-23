package sidecarcredentials

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite/pglitetest"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/hoophq/hoop/sidecar/daemon"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) { os.Exit(pglitetest.Main(m)) }

const testOrgID = "00000000-0000-0000-0000-0000000000d1"

func startTestDB(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	inst := pglitetest.StartMigrated(t)
	require.NoError(t, models.InitDatabaseConnection(inst.DSN(), 1))
	require.NoError(t, models.DB.Exec(
		`INSERT INTO private.orgs (id, name) VALUES (?, 'sidecar-credentials-api-test')`, testOrgID).Error)
}

type testUser struct {
	id, email string
	groups    []string
}

func seedUser(t *testing.T, subject string, groups ...string) testUser {
	t.Helper()
	email := subject + "@hoop.dev"
	require.NoError(t, models.DB.Exec(`INSERT INTO private.users (org_id, subject, email, name, status)
		VALUES (?, ?, ?, ?, 'active')`, testOrgID, subject, email, subject).Error)
	var id string
	require.NoError(t, models.DB.Raw(`SELECT id FROM private.users WHERE org_id = ? AND subject = ?`,
		testOrgID, subject).Scan(&id).Error)
	return testUser{id: id, email: email, groups: groups}
}

func seedSidecar(t *testing.T, name string, capabilities []string) *models.Sidecar {
	t.Helper()
	sc := &models.Sidecar{
		OrgID: testOrgID, Name: name, KeyHash: models.HashAPIKey("hsc_" + name), CreatedBy: "tests@hoop.dev",
		Configuration: models.SidecarConfiguration(daemon.Config{Listeners: []daemon.ListenerConfig{
			{Name: "appdb", Protocol: "postgres", Listen: "127.0.0.1:15432", Upstream: "db:5432"},
		}}),
	}
	require.NoError(t, models.CreateSidecar(models.DB, sc))
	if capabilities != nil {
		require.NoError(t, models.RecordSidecarHandshake(models.DB, sc.ID, "1.0.0", "", "", "", "rev", capabilities))
	}
	return sc
}

// call runs handler as user with params, through the same handler the API
// mounts.
func call(user testUser, method, body string, params gin.Params, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	ctx := storagev2.NewContext(user.id, testOrgID).WithUserInfo(user.email, user.email, "active", "", user.groups)
	c.Set(storagev2.ContextKey, ctx)
	c.Params = params
	c.Request = httptest.NewRequest(method, "/api/x", strings.NewReader(body))
	if body != "" {
		c.Request.Header.Set("Content-Type", "application/json")
	}
	handler(c)
	// gin writes a status set with no body when the chain ends; a handler
	// called directly has no chain.
	c.Writer.WriteHeaderNow()
	return rec
}

func laneParams(sidecar, lane string) gin.Params {
	return gin.Params{{Key: "nameOrID", Value: sidecar}, {Key: "lane", Value: lane}}
}

func TestMintNeedsAGrant(t *testing.T) {
	startTestDB(t)
	admin := seedUser(t, "admin", types.GroupAdmin)
	jane := seedUser(t, "jane", "dba")
	bob := seedUser(t, "bob", "sre")
	sc := seedSidecar(t, "pay", []string{"credential_set"})

	rec := call(jane, http.MethodPost, "", laneParams("pay", "appdb"), Mint)
	assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())

	rec = call(admin, http.MethodPost, `{"group_name":"dba"}`, laneParams("pay", "appdb"), CreateGrant)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var grant openapi.SidecarLaneGrant
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &grant))
	assert.Equal(t, "dba", grant.GroupName)
	assert.Equal(t, sc.ID, grant.SidecarID)
	assert.Equal(t, admin.email, grant.CreatedBy)

	rec = call(admin, http.MethodPost, `{"subject":"bob@hoop.dev","group_name":"x"}`, laneParams("pay", "appdb"), CreateGrant)
	assert.Equal(t, http.StatusBadRequest, rec.Code, "one of the two, not both")
	rec = call(admin, http.MethodPost, `{"subject":"bob@hoop.dev"}`, laneParams("pay", "nope"), CreateGrant)
	assert.Equal(t, http.StatusNotFound, rec.Code, "unknown lane")

	t.Run("by group", func(t *testing.T) {
		rec := call(jane, http.MethodPost, `{"expires_in_hours":2}`, laneParams("pay", "appdb"), Mint)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		var cred openapi.SidecarCredential
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &cred))
		assert.True(t, strings.HasPrefix(cred.Key, "hlc_"), cred.Key)
		assert.Equal(t, "pay", cred.SidecarName)
		assert.Equal(t, "appdb", cred.ListenerName)
		assert.Equal(t, "postgres", cred.Protocol)
		assert.Equal(t, jane.email, cred.User)
		assert.WithinDuration(t, time.Now().Add(2*time.Hour), cred.ExpiresAt, time.Minute)

		row, err := models.GetSidecarCredential(models.DB, testOrgID, cred.ID)
		require.NoError(t, err)
		assert.Equal(t, models.HashAPIKey(cred.Key), row.SecretKeyHash)
		assert.NotNil(t, row.ScramSHA256)
	})

	t.Run("by subject", func(t *testing.T) {
		rec := call(bob, http.MethodPost, "", laneParams("pay", "appdb"), Mint)
		assert.Equal(t, http.StatusForbidden, rec.Code)
		rec = call(admin, http.MethodPost, `{"subject":"bob@hoop.dev"}`, laneParams("pay", "appdb"), CreateGrant)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		rec = call(bob, http.MethodPost, "", laneParams(sc.ID, "appdb"), Mint)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		var cred openapi.SidecarCredential
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &cred))
		assert.WithinDuration(t, time.Now().Add(24*time.Hour), cred.ExpiresAt, time.Minute, "the default life")
	})

	t.Run("bounds", func(t *testing.T) {
		rec := call(jane, http.MethodPost, `{"expires_in_hours":721}`, laneParams("pay", "appdb"), Mint)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		rec = call(jane, http.MethodPost, `{"expires_in_hours":-1}`, laneParams("pay", "appdb"), Mint)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		rec = call(jane, http.MethodPost, "", laneParams("pay", "nope"), Mint)
		assert.Equal(t, http.StatusNotFound, rec.Code)
		rec = call(jane, http.MethodPost, "", laneParams("nope", "appdb"), Mint)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("a build without credential_set is refused", func(t *testing.T) {
		old := seedSidecar(t, "old", []string{"mcp"})
		rec := call(admin, http.MethodPost, `{"group_name":"dba"}`, laneParams(old.Name, "appdb"), CreateGrant)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		rec = call(jane, http.MethodPost, "", laneParams(old.Name, "appdb"), Mint)
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())

		// Never handshaked: support unknown, the handshake decides.
		fresh := seedSidecar(t, "fresh", nil)
		rec = call(admin, http.MethodPost, `{"group_name":"dba"}`, laneParams(fresh.Name, "appdb"), CreateGrant)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		rec = call(jane, http.MethodPost, "", laneParams(fresh.Name, "appdb"), Mint)
		assert.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	})

	t.Run("grants list and delete", func(t *testing.T) {
		rec := call(admin, http.MethodGet, "", laneParams("pay", "appdb"), ListGrants)
		require.Equal(t, http.StatusOK, rec.Code)
		var grants []openapi.SidecarLaneGrant
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &grants))
		require.Len(t, grants, 2)

		params := append(laneParams("pay", "appdb"), gin.Param{Key: "id", Value: grants[1].ID})
		rec = call(admin, http.MethodDelete, "", params, DeleteGrant)
		assert.Equal(t, http.StatusNoContent, rec.Code)
		rec = call(admin, http.MethodDelete, "", params, DeleteGrant)
		assert.Equal(t, http.StatusNotFound, rec.Code)
		rec = call(bob, http.MethodPost, "", laneParams("pay", "appdb"), Mint)
		assert.Equal(t, http.StatusForbidden, rec.Code, "the grant is gone")
	})
}

func TestListAndRevoke(t *testing.T) {
	startTestDB(t)
	admin := seedUser(t, "admin", types.GroupAdmin)
	jane := seedUser(t, "jane", "dba")
	bob := seedUser(t, "bob", "dba")
	seedSidecar(t, "pay", []string{"credential_set"})
	rec := call(admin, http.MethodPost, `{"group_name":"dba"}`, laneParams("pay", "appdb"), CreateGrant)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	mint := func(u testUser) openapi.SidecarCredential {
		rec := call(u, http.MethodPost, "", laneParams("pay", "appdb"), Mint)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		var cred openapi.SidecarCredential
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &cred))
		return cred
	}
	janes := mint(jane)
	janes2 := mint(jane)
	bobs := mint(bob)

	rec = call(jane, http.MethodGet, "", nil, ListMine)
	require.Equal(t, http.StatusOK, rec.Code)
	var mine []openapi.SidecarCredential
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &mine))
	require.Len(t, mine, 2)
	for _, m := range mine {
		assert.Empty(t, m.Key, "the key is shown once")
		assert.Equal(t, "pay", m.SidecarName)
		assert.Equal(t, "appdb", m.ListenerName)
		assert.Equal(t, jane.email, m.User)
	}
	assert.NotContains(t, rec.Body.String(), janes.Key)

	idParam := func(id string) gin.Params { return gin.Params{{Key: "id", Value: id}} }
	rec = call(jane, http.MethodDelete, "", idParam(bobs.ID), Revoke)
	assert.Equal(t, http.StatusNotFound, rec.Code, "another user's credential reads as absent")
	rec = call(jane, http.MethodDelete, "", idParam(janes.ID), Revoke)
	assert.Equal(t, http.StatusNoContent, rec.Code)
	rec = call(jane, http.MethodDelete, "", idParam(janes.ID), Revoke)
	assert.Equal(t, http.StatusNoContent, rec.Code, "revoking twice is fine")
	rec = call(admin, http.MethodDelete, "", idParam(bobs.ID), Revoke)
	assert.Equal(t, http.StatusNoContent, rec.Code, "an admin revokes any")

	rec = call(jane, http.MethodGet, "", nil, ListMine)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &mine))
	require.Len(t, mine, 1)
	assert.Equal(t, janes2.ID, mine[0].ID)
	rec = call(bob, http.MethodGet, "", nil, ListMine)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &mine))
	assert.Empty(t, mine)
}
