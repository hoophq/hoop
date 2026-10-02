package apisidecar

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func postSidecar(t *testing.T, name, config string) (*httptest.ResponseRecorder, openapi.SidecarCreateResponse) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/sidecars",
		bytes.NewReader([]byte(`{"name":"`+name+`","configuration":`+config+`}`)))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(storagev2.ContextKey, storagev2.NewContext("user-1", switchOrgID).
		WithUserInfo("Admin", "admin@hoop.dev", "active", "", nil))
	Post(c)
	var resp openapi.SidecarCreateResponse
	if w.Code == http.StatusCreated {
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	}
	return w, resp
}

// deleteSidecar flushes the status gin defers on a bodiless answer, as the
// handler chain does in production.
func deleteSidecar(t *testing.T, nameOrID string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/sidecars/"+nameOrID, nil)
	c.Set(storagev2.ContextKey, storagev2.NewContext("user-1", switchOrgID).
		WithUserInfo("Admin", "admin@hoop.dev", "active", "", nil))
	c.Params = gin.Params{{Key: "nameOrID", Value: nameOrID}}
	Delete(c)
	c.Writer.WriteHeaderNow()
	return w
}

// mirrorNames lists the connections a sidecar manages, with the type each
// one projects to.
func mirrorNames(t *testing.T, sidecarID string) map[string]string {
	t.Helper()
	var rows []struct {
		Name string `gorm:"column:name"`
		Kind string `gorm:"column:kind"`
	}
	require.NoError(t, models.DB.Raw(`SELECT name, type || '/' || subtype AS kind FROM private.connections
		WHERE org_id = ? AND sidecar_id = ? AND managed_by = 'sidecar'`, switchOrgID, sidecarID).Scan(&rows).Error)
	out := map[string]string{}
	for _, r := range rows {
		out[r.Name] = r.Kind
	}
	return out
}

// mirrorsOn turns beta.sidecar_listeners on for the test org: the projection
// is paused while it is off.
func mirrorsOn(t *testing.T) {
	t.Helper()
	featureflag.Set(switchOrgID, featureflag.FlagSidecarListeners, true)
	t.Cleanup(func() { featureflag.Set(switchOrgID, featureflag.FlagSidecarListeners, false) })
}

// seedConnection is a connection an admin made, on its own resource.
func seedConnection(t *testing.T, name string) {
	t.Helper()
	require.NoError(t, models.DB.Exec(`INSERT INTO private.resources (org_id, name, type, subtype) VALUES (?, ?, 'custom', 'loki')`, switchOrgID, name).Error)
	require.NoError(t, models.DB.Exec(`INSERT INTO private.connections (org_id, name, type, subtype, resource_name) VALUES (?, ?, 'custom', 'loki', ?)`, switchOrgID, name, name).Error)
}

// An org with the flag off is every org that uses sidecars today. Its sidecar
// writes and imports must answer as they did before the mirrors existed, with
// listener names the connection rule refuses and names a connection already
// has, and no connection may appear.
func TestWithTheFlagOffSidecarWritesAreUnchanged(t *testing.T) {
	startSwitchDB(t)
	seedConnection(t, "off-appdb")

	w, created := postSidecar(t, "off", `{"listeners": [
		{"name": "app db", "protocol": "postgres", "listen": ":5432", "upstream": "db:5432"},
		{"name": "appdb", "protocol": "postgres", "listen": ":5433", "upstream": "db:5432"}]}`)
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body)
	assert.Empty(t, mirrorNames(t, created.ID))

	w, _ = callAdmin(t, Put, http.MethodPut, created.ID, `{"listeners": [
		{"name": "appdb", "protocol": "mysql", "listen": ":3306", "upstream": "db:3306"}]}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	assert.Empty(t, mirrorNames(t, created.ID))

	sc := importedSidecar(t, "off-imp")
	assert.Empty(t, mirrorNames(t, sc.ID))

	w = deleteSidecar(t, created.ID)
	require.Equal(t, http.StatusNoContent, w.Code, "body: %s", w.Body)
}

func TestEverySidecarWriteKeepsItsMirrors(t *testing.T) {
	startSwitchDB(t)
	mirrorsOn(t)

	w, created := postSidecar(t, "pay", `{"listeners": [
		{"name": "appdb", "protocol": "postgres", "listen": ":5432", "upstream": "db:5432"},
		{"name": "api", "protocol": "http", "listen": ":8080", "upstream": "api:80"}]}`)
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body)
	assert.Equal(t, map[string]string{"pay-appdb": "database/postgres", "pay-api": "httpproxy/httpproxy"}, mirrorNames(t, created.ID))

	t.Run("PUT replaces the set", func(t *testing.T) {
		w, _ := callAdmin(t, Put, http.MethodPut, created.ID, `{"listeners": [
			{"name": "appdb", "protocol": "mysql", "listen": ":3306", "upstream": "db:3306"},
			{"name": "cache", "protocol": "clickhouse", "listen": ":9000", "upstream": "ch:9000"}]}`)
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
		assert.Equal(t, map[string]string{"pay-appdb": "database/mysql", "pay-cache": "custom/clickhouse"}, mirrorNames(t, created.ID))
	})

	// Puts the two listeners above back, so the next subtest starts from them.
	restore := func(t *testing.T) {
		t.Helper()
		w, _ := callAdmin(t, Put, http.MethodPut, created.ID, `{"listeners": [
			{"name": "appdb", "protocol": "mysql", "listen": ":3306", "upstream": "db:3306"},
			{"name": "cache", "protocol": "clickhouse", "listen": ":9000", "upstream": "ch:9000"}]}`)
		require.Equal(t, http.StatusOK, w.Code, "restore: %s", w.Body)
	}

	t.Run("a listener name the connection rule refuses takes the fallback name", func(t *testing.T) {
		w, _ := callAdmin(t, Put, http.MethodPut, created.ID, `{"listeners": [
			{"name": "app db", "protocol": "postgres", "listen": ":5432", "upstream": "db:5432"}]}`)
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
		assert.Equal(t, map[string]string{models.SidecarMirrorFallbackName("pay-app db", created.ID, "app db"): "database/postgres"},
			mirrorNames(t, created.ID))
		restore(t)
	})

	t.Run("a name another connection has takes the fallback name", func(t *testing.T) {
		seedConnection(t, "pay-logs")
		w, _ := callAdmin(t, Put, http.MethodPut, created.ID, `{"listeners": [
			{"name": "logs", "protocol": "http", "listen": ":3100", "upstream": "loki:3100"}]}`)
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
		assert.Equal(t, map[string]string{models.SidecarMirrorFallbackName("pay-logs", created.ID, "logs"): "httpproxy/httpproxy"},
			mirrorNames(t, created.ID))
		restore(t)
	})

	t.Run("the switch to the file keeps them, the switch back resets them", func(t *testing.T) {
		w, _ := callAdmin(t, Patch, http.MethodPatch, created.ID, `{"load_from_disk": true}`)
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
		assert.Len(t, mirrorNames(t, created.ID), 2, "the stored listeners still exist")

		w, _ = callAdmin(t, Patch, http.MethodPatch, created.ID, `{"load_from_disk": false}`)
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
		assert.Empty(t, mirrorNames(t, created.ID), "an emptied document has no listeners to mirror")
	})

	t.Run("DELETE takes them with the sidecar", func(t *testing.T) {
		w, _ := callAdmin(t, Put, http.MethodPut, created.ID, `{"listeners": [
			{"name": "appdb", "protocol": "postgres", "listen": ":5432", "upstream": "db:5432"}]}`)
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
		w = deleteSidecar(t, created.ID)
		require.Equal(t, http.StatusNoContent, w.Code, "body: %s", w.Body)
		assert.Empty(t, mirrorNames(t, created.ID))
		var resources int64
		models.DB.Raw(`SELECT count(*) FROM private.resources WHERE org_id = ? AND name = 'pay-appdb'`, switchOrgID).Scan(&resources)
		assert.Zero(t, resources, "the mirror's resource goes with it")
	})
}

func TestAnImportMirrorsTheListenersItBrings(t *testing.T) {
	startSwitchDB(t)
	mirrorsOn(t)
	sc := importedSidecar(t, "imp")
	assert.Equal(t, map[string]string{"imp-appdb": "database/postgres"}, mirrorNames(t, sc.ID))
}

// An import is how a sidecar seeds an empty plane with its file. The file is
// the source of truth and cannot be renamed from here, so a name another
// connection has must not refuse it.
func TestAnImportWithANameInUseStillImports(t *testing.T) {
	startSwitchDB(t)
	mirrorsOn(t)
	seedConnection(t, "clash-appdb")
	sc := importedSidecar(t, "clash")

	stored, err := models.GetSidecarByNameOrID(models.DB, switchOrgID, sc.ID)
	require.NoError(t, err)
	assert.Len(t, stored.Configuration.Listeners, 1, "the import stored the file")
	assert.Equal(t, map[string]string{models.SidecarMirrorFallbackName("clash-appdb", sc.ID, "appdb"): "database/postgres"},
		mirrorNames(t, sc.ID))
}
