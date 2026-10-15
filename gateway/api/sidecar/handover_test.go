package apisidecar

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newSidecar is a sidecar an admin created and nothing configured yet.
func newSidecar(t *testing.T, name string) *models.Sidecar {
	t.Helper()
	sc := &models.Sidecar{OrgID: switchOrgID, Name: name, KeyHash: models.HashAPIKey("hsc_" + name), CreatedBy: "tests@hoop.dev"}
	require.NoError(t, models.CreateSidecar(models.DB, sc))
	return sc
}

// reportHandshake runs the sidecar's handshake with body, as the row the
// middleware loads it.
func reportHandshake(t *testing.T, sc *models.Sidecar, body string) *httptest.ResponseRecorder {
	t.Helper()
	row, err := models.GetSidecarByNameOrID(models.DB, switchOrgID, sc.ID)
	require.NoError(t, err)
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/sidecars/handshake", bytes.NewReader([]byte(body)))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("sidecar-auth", row)
	Handshake(c)
	return w
}

func reportBody(t *testing.T, file string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"version": "1.0.0", "local_configuration": json.RawMessage(file)})
	require.NoError(t, err)
	return string(body)
}

// A sidecar that reports its file keeps it: the plane answers load_from_disk,
// shows the file, and holds no listener and no rule of it.
func TestAReportingSidecarKeepsItsFile(t *testing.T) {
	startSwitchDB(t)
	sc := newSidecar(t, "keeps-file")

	w := reportHandshake(t, sc, reportBody(t, switchFile))
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	assert.JSONEq(t, `{"load_from_disk":true}`, w.Body.String())

	w, resp := callAdmin(t, Get, http.MethodGet, sc.ID, `{}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	assert.Empty(t, resp.Configuration.Listeners, "the plane took nothing")
	require.NotNil(t, resp.Configuration.LoadFromDisk)
	assert.True(t, *resp.Configuration.LoadFromDisk)
	require.NotNil(t, resp.ReportedConfiguration, "the page shows the file")
	require.Len(t, resp.ReportedConfiguration.Listeners, 1)
	assert.Equal(t, "appdb", resp.ReportedConfiguration.Listeners[0].Name)
	assert.NotNil(t, resp.LastSeenAt, "a sidecar running its file is running")

	bound, err := models.ListSidecarRuleBindings(models.DB, uuid.MustParse(switchOrgID), sc.ID)
	require.NoError(t, err)
	assert.Empty(t, bound, "no rule is imported before the admin moves the configuration")

	// The next heartbeats change nothing.
	w = reportHandshake(t, sc, reportBody(t, switchFile))
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	assert.JSONEq(t, `{"load_from_disk":true}`, w.Body.String())
}

// The admin's move adopts the reported file: its rules become rule items and
// the next handshake serves its listeners.
func TestMovingTheConfigurationAdoptsTheReportedFile(t *testing.T) {
	startSwitchDB(t)
	sc := newSidecar(t, "moves")
	require.Equal(t, http.StatusOK, reportHandshake(t, sc, reportBody(t, switchFile)).Code)

	w, resp := callAdmin(t, Patch, http.MethodPatch, sc.ID, `{"load_from_disk": false}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	require.Len(t, resp.Configuration.Listeners, 1)
	assert.Nil(t, resp.ReportedConfiguration, "the report is the plane's document now")
	assert.ElementsMatch(t, []string{"moves-appdb-own", "moves-appdb-emails"}, boundNames(t, sc.ID))

	// A plane-owned sidecar still sends its file at boot; it is not kept.
	w = reportHandshake(t, sc, reportBody(t, switchFile))
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	var served map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &served))
	assert.NotContains(t, served, "load_from_disk")
	assert.Contains(t, served, "listeners")
	_, resp = callAdmin(t, Get, http.MethodGet, sc.ID, `{}`)
	assert.Nil(t, resp.ReportedConfiguration)
}

// A build older than the report sends none and gets the 412 that makes it
// push its file, as before: its boot fails without that import.
func TestASidecarWithoutAReportStillGets412(t *testing.T) {
	startSwitchDB(t)
	sc := newSidecar(t, "old-build")
	w := reportHandshake(t, sc, `{"version":"1.0.0"}`)
	assert.Equal(t, http.StatusPreconditionFailed, w.Code, "body: %s", w.Body)
}

// A report this plane cannot adopt is not kept and changes nothing: the 412
// stays, and the sidecar imports as an older build does.
func TestAnInvalidReportIsIgnored(t *testing.T) {
	startSwitchDB(t)
	sc := newSidecar(t, "bad-report")
	w := reportHandshake(t, sc, reportBody(t, `{"listeners":[],"unknown_key":1}`))
	assert.Equal(t, http.StatusPreconditionFailed, w.Code, "body: %s", w.Body)
	_, resp := callAdmin(t, Get, http.MethodGet, sc.ID, `{}`)
	assert.Nil(t, resp.ReportedConfiguration)
	assert.Nil(t, resp.Configuration.LoadFromDisk)
}

// The report never replaces a configuration an admin authored.
func TestAReportNeverReplacesAStoredConfiguration(t *testing.T) {
	startSwitchDB(t)
	sc := importedSidecar(t, "authored")
	w := reportHandshake(t, sc, reportBody(t, `{"listeners":[{"name":"other","protocol":"postgres","listen":":1","upstream":"h:5432"}]}`))
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	_, resp := callAdmin(t, Get, http.MethodGet, sc.ID, `{}`)
	require.Len(t, resp.Configuration.Listeners, 1)
	assert.Equal(t, "appdb", resp.Configuration.Listeners[0].Name)
	assert.Nil(t, resp.ReportedConfiguration)
}

func boundNames(t *testing.T, sidecarID string) []string {
	t.Helper()
	bound, err := models.ListSidecarRuleBindings(models.DB, uuid.MustParse(switchOrgID), sidecarID)
	require.NoError(t, err)
	var names []string
	for _, b := range bound {
		names = append(names, b.RuleName)
	}
	return names
}
