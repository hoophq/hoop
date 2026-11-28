package apiscim_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/gateway/api/openapi"
	apiscim "github.com/hoophq/hoop/gateway/api/scim"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite/pglitetest"
	"github.com/hoophq/hoop/gateway/storagev2"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const scimTestOrgID = "00000000-0000-4000-8000-0000000000d1"

func TestMain(m *testing.M) {
	os.Setenv("API_URL", "http://localhost:8009")
	_ = appconfig.Load()
	os.Exit(pglitetest.Main(m))
}

func startSCIMDB(t *testing.T) *gin.Engine {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	inst := pglitetest.StartMigrated(t)
	require.NoError(t, models.InitDatabaseConnection(inst.DSN(), 1))
	require.NoError(t, models.DB.Exec(`INSERT INTO private.orgs (id, name) VALUES (?, 'scim-test')`, scimTestOrgID).Error)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := r.Group("/api")
	apiscim.RegisterSCIMGroup(api)
	return r
}

func adminCall(t *testing.T, handler gin.HandlerFunc, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set(storagev2.ContextKey, storagev2.NewContext("admin-sub", scimTestOrgID).
		WithUserInfo("Admin", "admin@example.com", "active", "", []string{"admin"}))
	c.Request = httptest.NewRequest(method, path, nil)
	handler(c)
	c.Writer.WriteHeaderNow()
	return rec
}

func scimCall(r *gin.Engine, token, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, "/api/scim/v2"+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/scim+json")
	}
	r.ServeHTTP(rec, req)
	return rec
}

func TestSCIMProvisioningEndToEnd(t *testing.T) {
	r := startSCIMDB(t)

	// Token creation and status
	stRec := adminCall(t, apiscim.GetTokenStatus, http.MethodGet, "/api/scim/token")
	require.Equal(t, http.StatusOK, stRec.Code)
	var st openapi.SCIMTokenStatus
	require.NoError(t, json.Unmarshal(stRec.Body.Bytes(), &st))
	assert.False(t, st.Enabled)
	assert.False(t, st.Configured)

	createTokRec := adminCall(t, apiscim.CreateOrRotateToken, http.MethodPost, "/api/scim/token")
	require.Equal(t, http.StatusCreated, createTokRec.Code)
	var tok openapi.SCIMTokenCreated
	require.NoError(t, json.Unmarshal(createTokRec.Body.Bytes(), &tok))
	assert.True(t, strings.HasPrefix(tok.Token, "hscim_"))

	// Flag off -> 403
	assert.Equal(t, http.StatusForbidden, scimCall(r, tok.Token, http.MethodGet, "/Users", "").Code)
	// Bad token -> 401
	assert.Equal(t, http.StatusUnauthorized, scimCall(r, "hscim_wrong", http.MethodGet, "/Users", "").Code)

	featureflag.Set(scimTestOrgID, featureflag.FlagSCIMProvisioning, true)
	t.Cleanup(func() { featureflag.Set(scimTestOrgID, featureflag.FlagSCIMProvisioning, false) })

	// Discovery
	assert.Equal(t, http.StatusOK, scimCall(r, tok.Token, http.MethodGet, "/ServiceProviderConfig", "").Code)
	assert.Equal(t, http.StatusOK, scimCall(r, tok.Token, http.MethodGet, "/ResourceTypes", "").Code)
	assert.Equal(t, http.StatusOK, scimCall(r, tok.Token, http.MethodGet, "/Schemas", "").Code)

	// Create two users via SCIM
	u1Rec := scimCall(r, tok.Token, http.MethodPost, "/Users",
		`{"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],"externalId":"ext-ana","userName":"Ana@Example.com","displayName":"Ana","active":true}`)
	require.Equal(t, http.StatusCreated, u1Rec.Code, u1Rec.Body.String())
	var u1 map[string]any
	require.NoError(t, json.Unmarshal(u1Rec.Body.Bytes(), &u1))
	u1ID := u1["id"].(string)
	assert.Equal(t, "ana@example.com", u1["userName"])

	u2Rec := scimCall(r, tok.Token, http.MethodPost, "/Users",
		`{"externalId":"ext-bo","userName":"bo@example.com","name":{"givenName":"Bo","familyName":"Lee"},"active":true}`)
	require.Equal(t, http.StatusCreated, u2Rec.Code, u2Rec.Body.String())
	var u2 map[string]any
	require.NoError(t, json.Unmarshal(u2Rec.Body.Bytes(), &u2))
	u2ID := u2["id"].(string)
	assert.Equal(t, "Bo Lee", u2["displayName"])

	// Filter by userName
	fRec := scimCall(r, tok.Token, http.MethodGet, `/Users?filter=userName%20eq%20%22ana@example.com%22`, "")
	require.Equal(t, http.StatusOK, fRec.Code)
	var listResp struct {
		TotalResults int              `json:"totalResults"`
		Resources    []map[string]any `json:"Resources"`
	}
	require.NoError(t, json.Unmarshal(fRec.Body.Bytes(), &listResp))
	assert.Equal(t, 1, listResp.TotalResults)
	assert.Equal(t, u1ID, listResp.Resources[0]["id"])

	// PatchOp deactivate user (Entra/PingFederate pathless form)
	patchRec := scimCall(r, tok.Token, http.MethodPatch, "/Users/"+u2ID,
		`{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[{"op":"replace","value":{"active":false}}]}`)
	require.Equal(t, http.StatusOK, patchRec.Code, patchRec.Body.String())
	var patched map[string]any
	require.NoError(t, json.Unmarshal(patchRec.Body.Bytes(), &patched))
	assert.Equal(t, false, patched["active"])

	// Re-activate via path form
	patchRec2 := scimCall(r, tok.Token, http.MethodPatch, "/Users/"+u2ID,
		`{"Operations":[{"op":"replace","path":"active","value":true}]}`)
	require.Equal(t, http.StatusOK, patchRec2.Code)

	// Create group with u1, then PATCH add u2, then PATCH remove u1 (PingFederate/Okta member filter)
	gRec := scimCall(r, tok.Token, http.MethodPost, "/Groups",
		`{"externalId":"g-dba","displayName":"dba","members":[{"value":"`+u1ID+`"}]}`)
	require.Equal(t, http.StatusCreated, gRec.Code, gRec.Body.String())
	var grp map[string]any
	require.NoError(t, json.Unmarshal(gRec.Body.Bytes(), &grp))
	grpID := grp["id"].(string)

	groupMembers := func(name string) []string {
		var ids []string
		require.NoError(t, models.DB.Raw(`SELECT user_id::text FROM private.user_groups WHERE org_id = ? AND name = ? ORDER BY user_id`, scimTestOrgID, name).Scan(&ids).Error)
		return ids
	}
	assert.Equal(t, []string{u1ID}, groupMembers("dba"))

	// Add u2
	pGrp := scimCall(r, tok.Token, http.MethodPatch, "/Groups/"+grpID,
		`{"Operations":[{"op":"add","path":"members","value":[{"value":"`+u2ID+`"}]}]}`)
	require.Equal(t, http.StatusOK, pGrp.Code, pGrp.Body.String())
	assert.ElementsMatch(t, []string{u1ID, u2ID}, groupMembers("dba"))

	// Remove u1 via RFC 7644 value filter & rename group to dba-leads
	pGrp2 := scimCall(r, tok.Token, http.MethodPatch, "/Groups/"+grpID,
		`{"Operations":[{"op":"remove","path":"members[value eq \"`+u1ID+`\"]"},{"op":"replace","path":"displayName","value":"dba-leads"}]}`)
	require.Equal(t, http.StatusOK, pGrp2.Code, pGrp2.Body.String())
	assert.Empty(t, groupMembers("dba"))
	assert.Equal(t, []string{u2ID}, groupMembers("dba-leads"))

	// Delete group cleans user_groups
	assert.Equal(t, http.StatusNoContent, scimCall(r, tok.Token, http.MethodDelete, "/Groups/"+grpID, "").Code)
	assert.Empty(t, groupMembers("dba-leads"))

	// Delete user deactivates
	assert.Equal(t, http.StatusNoContent, scimCall(r, tok.Token, http.MethodDelete, "/Users/"+u1ID, "").Code)
	var status string
	require.NoError(t, models.DB.Raw(`SELECT status FROM private.users WHERE id = ?`, u1ID).Scan(&status).Error)
	assert.Equal(t, "inactive", status)

	// Revoke token -> 401
	delRec := adminCall(t, apiscim.DeleteToken, http.MethodDelete, "/api/scim/token")
	assert.Equal(t, http.StatusNoContent, delRec.Code)
	assert.Equal(t, http.StatusUnauthorized, scimCall(r, tok.Token, http.MethodGet, "/Users", "").Code)
}
