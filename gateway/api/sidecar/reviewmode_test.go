package apisidecar

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reviewModeConfig is a lane that holds for approval in the given mode.
func reviewModeConfig(mode string) string {
	modeKey := ""
	if mode != "" {
		modeKey = `, "review_mode": "` + mode + `"`
	}
	return `{"analyzer": {"provider": "anthropic", "model": "m"}, "listeners": [{
		"name": "agents", "protocol": "postgres", "listen": ":5432", "upstream": "db:5432",
		"analyzer": {"high": "require_review", "approval_rule": "payments"` + modeKey + `}
	}]}`
}

func handshake(t *testing.T, sc *models.Sidecar, capabilities string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/sidecars/handshake",
		bytes.NewReader([]byte(`{"version": "1.196.0"}`)))
	c.Request.Header.Set("Content-Type", "application/json")
	if capabilities != "" {
		c.Request.Header.Set(daemon.CapabilitiesHeader, capabilities)
	}
	c.Set("sidecar-auth", sc)
	Handshake(c)
	return w
}

// A hold lane serves no review_mode key, so a build that predates the field
// keeps decoding its document.
func TestAHoldLaneIsServedWithoutTheReviewModeKey(t *testing.T) {
	stored := models.SidecarConfiguration(daemon.Config{Listeners: []daemon.ListenerConfig{{
		Name:     "appdb",
		Analyzer: &daemon.LaneAnalyzerConfig{HighRisk: "require_review", ReviewMode: analyzer.ReviewHold},
	}}})
	raw, err := json.Marshal(servedConfig(stored, nil))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "review_mode")
	assert.Equal(t, analyzer.ReviewHold, stored.Listeners[0].Analyzer.ReviewMode,
		"the stored row keeps what the admin wrote")
}

func TestReturnIsServedOnlyToASidecarThatReportsIt(t *testing.T) {
	startSwitchDB(t)
	sc := &models.Sidecar{OrgID: switchOrgID, Name: "agents-edge",
		KeyHash: models.HashAPIKey("hsc_agents"), CreatedBy: "tests@hoop.dev"}
	require.NoError(t, models.CreateSidecar(models.DB, sc))
	cfg, err := services.ParseSidecarConfiguration(json.RawMessage(reviewModeConfig("return")))
	require.NoError(t, err)
	stored, err := models.UpdateSidecarConfiguration(models.DB, switchOrgID, sc.ID, models.SidecarConfiguration(cfg))
	require.NoError(t, err)

	w := handshake(t, stored, "")
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body: %s", w.Body)
	assert.Contains(t, w.Body.String(), "1.196.0")
	assert.Contains(t, w.Body.String(), `listener \"agents\"`)

	w = handshake(t, stored, daemon.CapabilityReviewMode)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	assert.True(t, strings.Contains(w.Body.String(), `"review_mode":"return"`), "body: %s", w.Body)

	got, err := models.GetSidecarByNameOrID(models.DB, switchOrgID, sc.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{daemon.CapabilityReviewMode}, []string(got.Capabilities))
}

func TestSettingReturnOnAnOldSidecarIsRefused(t *testing.T) {
	startSwitchDB(t)
	sc := &models.Sidecar{OrgID: switchOrgID, Name: "old-edge",
		KeyHash: models.HashAPIKey("hsc_old"), CreatedBy: "tests@hoop.dev"}
	require.NoError(t, models.CreateSidecar(models.DB, sc))

	// Never handshaked: support is unknown, and the handshake gates instead.
	w, _ := callAdmin(t, Put, http.MethodPut, sc.ID, reviewModeConfig("return"))
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)

	// Handshaked without the header: a build that predates the field.
	require.NoError(t, models.RecordSidecarHandshake(models.DB, sc.ID, "1.190.0", "", "", "", "", nil))
	w, _ = callAdmin(t, Put, http.MethodPut, sc.ID, reviewModeConfig("return"))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body: %s", w.Body)
	assert.Contains(t, w.Body.String(), "1.196.0")

	w, _ = callAdmin(t, Put, http.MethodPut, sc.ID, reviewModeConfig("hold"))
	require.Equal(t, http.StatusOK, w.Code, "a hold lane stays savable: %s", w.Body)
}

// The plane runs the sidecar's own analyzer check before storing, so a block
// every sidecar would refuse is a 422 at the save and not a refused document.
func TestAnAnalyzerBlockTheSidecarRefusesIsNotSaved(t *testing.T) {
	startSwitchDB(t)
	sc := &models.Sidecar{OrgID: switchOrgID, Name: "typo-edge",
		KeyHash: models.HashAPIKey("hsc_typo"), CreatedBy: "tests@hoop.dev"}
	require.NoError(t, models.CreateSidecar(models.DB, sc))

	w, _ := callAdmin(t, Put, http.MethodPut, sc.ID, reviewModeConfig("Return"))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body: %s", w.Body)
	assert.Contains(t, w.Body.String(), "unknown review_mode")

	w, _ = callAdmin(t, Put, http.MethodPut, sc.ID, `{"analyzer": {"provider": "anthropic", "model": "m"}, "listeners": [{
		"name": "agents", "protocol": "postgres", "listen": ":5432", "upstream": "db:5432",
		"analyzer": {"high": "block", "review_mode": "return"}}]}`)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body: %s", w.Body)
	assert.Contains(t, w.Body.String(), "nothing on this lane would hold one")
}

// A refused handshake still records what the build reported, so the next save
// knows it is too old.
func TestARefusedHandshakeRecordsTheCapabilities(t *testing.T) {
	startSwitchDB(t)
	sc := &models.Sidecar{OrgID: switchOrgID, Name: "first-edge",
		KeyHash: models.HashAPIKey("hsc_first"), CreatedBy: "tests@hoop.dev"}
	require.NoError(t, models.CreateSidecar(models.DB, sc))
	w, _ := callAdmin(t, Put, http.MethodPut, sc.ID, reviewModeConfig("return"))
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	stored, err := models.GetSidecarByNameOrID(models.DB, switchOrgID, sc.ID)
	require.NoError(t, err)

	w = handshake(t, stored, "")
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body: %s", w.Body)

	got, err := models.GetSidecarByNameOrID(models.DB, switchOrgID, sc.ID)
	require.NoError(t, err)
	assert.NotNil(t, got.Capabilities, "a refused build reads as known, not as never seen")
	assert.Empty(t, got.Capabilities)
	assert.Nil(t, got.LastSeenAt, "a sidecar that cannot run is not recently seen")

	w, _ = callAdmin(t, Put, http.MethodPut, sc.ID, reviewModeConfig("return"))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body: %s", w.Body)
}
