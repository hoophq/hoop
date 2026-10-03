package sidecarserviceaccounts

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	modelsbootstrap "github.com/hoophq/hoop/gateway/models/bootstrap"
	"github.com/hoophq/hoop/gateway/pglite"
	"github.com/hoophq/hoop/gateway/storagev2"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testOrgID  = "00000000-0000-0000-0000-0000000000d1"
	otherOrgID = "00000000-0000-0000-0000-0000000000d2"
)

func startDB(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	ctx := context.Background()
	inst, err := pglite.Start(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { inst.Close(ctx) })
	require.NoError(t, modelsbootstrap.MigrateDB(inst.MigrateDSN(), ""))
	// The embedded backend serves one session at a time.
	require.NoError(t, models.InitDatabaseConnection(inst.DSN(), 1))
	sqlDB, err := models.DB.DB()
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	for _, org := range []string{testOrgID, otherOrgID} {
		require.NoError(t, models.DB.Exec(`INSERT INTO private.orgs (id, name) VALUES (?, ?)`, org, "sa-test-"+org[len(org)-2:]).Error)
	}
}

func call(t *testing.T, handler gin.HandlerFunc, method string, params gin.Params, body string) *httptest.ResponseRecorder {
	t.Helper()
	return callAs(t, testOrgID, handler, method, params, body)
}

func callAs(t *testing.T, orgID string, handler gin.HandlerFunc, method string, params gin.Params, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, "/api/sidecar-service-accounts", bytes.NewReader([]byte(body)))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(storagev2.ContextKey, storagev2.NewContext("user-1", orgID).
		WithUserInfo("Admin", "admin@hoop.dev", "active", "", nil))
	c.Params = params
	handler(c)
	// gin writes a status set with no body when the chain ends; a handler
	// called directly has no chain.
	c.Writer.WriteHeaderNow()
	return w
}

const k8sMapping = `{"name": "gke-eu",
	"issuer": "https://container.googleapis.com/v1/projects/p/locations/eu/clusters/eu",
	"audience": "https://hoop.example.com", "claim": "sub",
	"subject_pattern": "system:serviceaccount:*:hoop-sidecar", "name_template": "gke-eu-{1}"}`

func TestSidecarServiceAccountsAPI(t *testing.T) {
	startDB(t)

	w := call(t, Create, http.MethodPost, nil, k8sMapping)
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body)
	var created openapi.SidecarServiceAccount
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	assert.NotEmpty(t, created.ID)
	assert.Equal(t, "admin@hoop.dev", created.CreatedBy)
	assert.Empty(t, created.JWKS, "no key set was sent")
	id := gin.Params{{Key: "id", Value: created.ID}}

	w = call(t, Create, http.MethodPost, nil, k8sMapping)
	assert.Equal(t, http.StatusConflict, w.Code, "same name: %s", w.Body)

	// The issuer and audience pair belongs to the organization that mapped
	// it first; a second pattern in that organization reuses it.
	w = callAs(t, otherOrgID, Create, http.MethodPost, nil, k8sMapping)
	assert.Equal(t, http.StatusConflict, w.Code, "another organization, same pair: %s", w.Body)
	assert.Contains(t, w.Body.String(), "already used by another organization")
	w = callAs(t, otherOrgID, Create, http.MethodPost, nil,
		strings.Replace(k8sMapping, `"https://hoop.example.com"`, `"https://hoop.example.com/org-b"`, 1))
	assert.Equal(t, http.StatusCreated, w.Code, "another organization, own audience: %s", w.Body)
	w = call(t, Create, http.MethodPost, nil, strings.NewReplacer(`"gke-eu"`, `"gke-eu-default"`,
		`:hoop-sidecar"`, `:default"`).Replace(k8sMapping))
	assert.Equal(t, http.StatusCreated, w.Code, "same organization, second pattern: %s", w.Body)

	for name, body := range map[string]string{
		"bare * without allow_any_subject": `{"name": "open", "issuer": "https://issuer.example.com", "audience": "a",
			"claim": "sub", "subject_pattern": "*", "name_template": "sc-{1}"}`,
		"google without a project": `{"name": "gcp", "issuer": "https://accounts.google.com", "audience": "a",
			"claim": "email", "subject_pattern": "*@gmail.com", "name_template": "sc-{1}"}`,
		"invalid jwks": `{"name": "static", "issuer": "kubernetes", "audience": "a", "jwks": {"keys": []},
			"claim": "sub", "subject_pattern": "x:*", "name_template": "sc-{1}"}`,
	} {
		w = call(t, Create, http.MethodPost, nil, body)
		assert.Equal(t, http.StatusUnprocessableEntity, w.Code, "%s: %s", name, w.Body)
	}
	w = call(t, Create, http.MethodPost, nil, `{"name": "missing-fields"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	w = call(t, Get, http.MethodGet, id, "")
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	w = call(t, Get, http.MethodGet, gin.Params{{Key: "id", Value: "8a4239fa-5116-4bbb-ad3c-ea1f294aac4a"}}, "")
	assert.Equal(t, http.StatusNotFound, w.Code)

	w = call(t, Update, http.MethodPut, id, `{"name": "gke-eu",
		"issuer": "https://container.googleapis.com/v1/projects/p/locations/eu/clusters/eu",
		"audience": "https://hoop2.example.com", "claim": "sub", "jwks": null,
		"subject_pattern": "system:serviceaccount:*:hoop-sidecar", "name_template": "gke-eu-{1}"}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	var updated openapi.SidecarServiceAccount
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &updated))
	assert.Equal(t, "https://hoop2.example.com", updated.Audience)
	assert.Equal(t, "admin@hoop.dev", updated.CreatedBy, "an update keeps who created the mapping")
	w = call(t, Update, http.MethodPut, gin.Params{{Key: "id", Value: "8a4239fa-5116-4bbb-ad3c-ea1f294aac4a"}}, k8sMapping)
	assert.Equal(t, http.StatusNotFound, w.Code)

	w = call(t, List, http.MethodGet, nil, "")
	require.Equal(t, http.StatusOK, w.Code)
	var items []openapi.SidecarServiceAccount
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &items))
	assert.Len(t, items, 2, "this organization's two mappings, not the other one's")

	w = call(t, Delete, http.MethodDelete, id, "")
	assert.Equal(t, http.StatusNoContent, w.Code)
	w = call(t, Delete, http.MethodDelete, id, "")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestSidecarDeletedNamesAPI(t *testing.T) {
	startDB(t)
	require.NoError(t, models.InsertSidecarDeletedName(models.DB, testOrgID, "gke-eu-ws-1", "admin@hoop.dev"))

	w := call(t, ListDeletedNames, http.MethodGet, nil, "")
	require.Equal(t, http.StatusOK, w.Code)
	var names []openapi.SidecarDeletedName
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &names))
	require.Len(t, names, 1)
	assert.Equal(t, "gke-eu-ws-1", names[0].Name)

	name := gin.Params{{Key: "name", Value: "gke-eu-ws-1"}}
	w = call(t, ClearDeletedName, http.MethodDelete, name, "")
	assert.Equal(t, http.StatusNoContent, w.Code)
	w = call(t, ClearDeletedName, http.MethodDelete, name, "")
	assert.Equal(t, http.StatusNotFound, w.Code)
}
