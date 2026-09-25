package apisidecar

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	modelsbootstrap "github.com/hoophq/hoop/gateway/models/bootstrap"
	"github.com/hoophq/hoop/gateway/pglite"
	"github.com/hoophq/hoop/gateway/storagev2"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const switchOrgID = "00000000-0000-0000-0000-0000000000b2"

const switchFile = `{"listeners": [{
	"name": "appdb", "protocol": "postgres", "listen": ":5432", "upstream": "db:5432",
	"guardrails": {"rules": [{"name": "own", "type": "deny_words_list", "words": ["x"]}]},
	"mask": {"rules": [{"name": "emails", "entities": ["EMAIL_ADDRESS"], "strategy": "redact"}]}
}]}`

// startSwitchDB boots the embedded database the way the gateway does, so the
// owner switch runs against the real schema and its row lock.
func startSwitchDB(t *testing.T) {
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
	require.NoError(t, models.DB.Exec(`INSERT INTO private.orgs (id, name) VALUES (?, 'switch-test')`, switchOrgID).Error)
}

// importedSidecar is a sidecar that pushed switchFile, through the handler.
func importedSidecar(t *testing.T, name string) *models.Sidecar {
	t.Helper()
	sc := &models.Sidecar{OrgID: switchOrgID, Name: name, KeyHash: models.HashAPIKey("hsc_" + name), CreatedBy: "tests@hoop.dev"}
	require.NoError(t, models.CreateSidecar(models.DB, sc))

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/sidecars/configuration", bytes.NewReader([]byte(switchFile)))
	c.Set("sidecar-auth", sc)
	ImportConfiguration(c)
	require.Equal(t, http.StatusOK, w.Code, "import: %s", w.Body)
	return sc
}

func callAdmin(t *testing.T, handler gin.HandlerFunc, method, nameOrID, config string) (*httptest.ResponseRecorder, openapi.SidecarResponse) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, "/api/sidecars/"+nameOrID,
		bytes.NewReader([]byte(`{"configuration":`+config+`}`)))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(storagev2.ContextKey, storagev2.NewContext("user-1", switchOrgID).
		WithUserInfo("Admin", "admin@hoop.dev", "active", "", nil))
	c.Params = gin.Params{{Key: "nameOrID", Value: nameOrID}}
	handler(c)
	var resp openapi.SidecarResponse
	if w.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	}
	return w, resp
}

func TestTheImportWritesTheRulesAndTheirBindings(t *testing.T) {
	startSwitchDB(t)
	sc := importedSidecar(t, "imp-edge")

	bound, err := models.ListSidecarRuleBindings(models.DB, uuid.MustParse(switchOrgID), sc.ID)
	require.NoError(t, err)
	var names []string
	for _, b := range bound {
		names = append(names, b.RuleName)
	}
	assert.ElementsMatch(t, []string{"imp-edge-appdb-own", "imp-edge-appdb-emails"}, names)
	var marked int64
	models.DB.Raw(`SELECT (SELECT count(*) FROM private.guardrail_rules WHERE imported_from_sidecar = ?) +
		(SELECT count(*) FROM private.datamasking_rules WHERE imported_from_sidecar = ?)`, sc.ID, sc.ID).Scan(&marked)
	assert.EqualValues(t, 2, marked, "every imported rule names its sidecar")
	stored, err := models.GetSidecarByNameOrID(models.DB, switchOrgID, sc.ID)
	require.NoError(t, err)
	require.Len(t, stored.Configuration.Listeners, 1)
	if g := stored.Configuration.Listeners[0].Guardrails; g != nil {
		assert.Empty(t, g.Rules, "the rules leave the stored document")
	}
}

func TestAPatchToTheFileReportsWhatItDetached(t *testing.T) {
	startSwitchDB(t)
	sc := importedSidecar(t, "to-file")
	require.NoError(t, models.RecordSidecarHandshake(models.DB, sc.ID, "1.2.3", "", "", ""))

	w, resp := callAdmin(t, Patch, http.MethodPatch, sc.ID, `{"load_from_disk": true}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	require.NotNil(t, resp.DetachedRules)
	assert.Equal(t, []string{"to-file-appdb-own"}, resp.DetachedRules.Deleted.Guardrails)
	assert.Equal(t, []string{"to-file-appdb-emails"}, resp.DetachedRules.Deleted.DataMasking)
	assert.Empty(t, resp.BoundRules)
	assert.Equal(t, "1.2.3", resp.Version, "the answer carries the runtime columns, as Get does")
}

func TestAPatchBackResetsTheDocument(t *testing.T) {
	startSwitchDB(t)
	sc := importedSidecar(t, "back-edge")
	w, _ := callAdmin(t, Patch, http.MethodPatch, sc.ID, `{"load_from_disk": true}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)

	w, resp := callAdmin(t, Patch, http.MethodPatch, sc.ID, `{"load_from_disk": false}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	assert.Empty(t, resp.Configuration.Listeners, "the file replaces the stored document")
	assert.NotNil(t, resp.DetachedRules)
}

func TestAPatchBackRefusesOtherKeys(t *testing.T) {
	w, _ := callAdmin(t, Patch, http.MethodPatch, "sc-a",
		`{"load_from_disk": false, "guardrails": {"mode": "observe"}}`)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body: %s", w.Body)
	assert.Contains(t, w.Body.String(), "load_from_disk false alone")
}

func TestPutRefusesALoadFromDiskChange(t *testing.T) {
	startSwitchDB(t)
	sc := importedSidecar(t, "put-edge")

	w, _ := callAdmin(t, Put, http.MethodPut, sc.ID, `{"load_from_disk": true, "listeners": [
		{"name": "appdb", "protocol": "postgres", "listen": ":5432", "upstream": "db:5432"}]}`)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body: %s", w.Body)
	assert.Contains(t, w.Body.String(), "use PATCH")
	bound, err := models.ListSidecarRuleBindings(models.DB, uuid.MustParse(switchOrgID), sc.ID)
	require.NoError(t, err)
	assert.Len(t, bound, 2, "a refused PUT detaches nothing")
}
