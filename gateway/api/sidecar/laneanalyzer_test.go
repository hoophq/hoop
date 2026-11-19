package apisidecar

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const laneAnalyzerFile = `{
	"analyzer": {"provider": "gemini", "model": "m"},
	"listeners": [{
		"name": "appdb", "protocol": "postgres", "listen": "0.0.0.0:15432", "upstream": "db:5432",
		"analyzer": {"trigger": {"operations": ["update"]}, "high": "block", "max_calls": 50}
	}]
}`

// A lane override stays on the lane as the base the bound rule merges over.
// The base names no action, and the write check reads it with the rule in.
func TestMovingAFileWithALaneAnalyzerOverrideAdoptsIt(t *testing.T) {
	startSwitchDB(t)
	sc := newSidecar(t, "lane-override")
	require.Equal(t, http.StatusOK, reportHandshake(t, sc, reportBody(t, laneAnalyzerFile)).Code)

	w, resp := callAdmin(t, Patch, http.MethodPatch, sc.ID, `{"load_from_disk": false}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	require.NotNil(t, resp.Configuration.Listeners[0].Analyzer)
	assert.Equal(t, 50, resp.Configuration.Listeners[0].Analyzer.MaxCalls)
	assert.ElementsMatch(t, []string{"lane-override-appdb-analyzer"}, boundNames(t, sc.ID))

	w = reportHandshake(t, sc, reportBody(t, laneAnalyzerFile))
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	var served struct {
		Listeners []struct {
			Analyzer map[string]any `json:"analyzer"`
		} `json:"listeners"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &served))
	assert.Equal(t, "block", served.Listeners[0].Analyzer["high"])
	assert.EqualValues(t, 50, served.Listeners[0].Analyzer["max_calls"])

	w, _ = callAdmin(t, Patch, http.MethodPatch, sc.ID, `{"log_level": "debug"}`)
	assert.Equal(t, http.StatusOK, w.Code, "a later edit is refused: %s", w.Body)
}

// An import before ENG-1232 stored `analyzer: {}` on the lane. The row stays
// editable while its rule is bound.
func TestAnEmptyLaneAnalyzerWithABoundRuleIsEditable(t *testing.T) {
	startSwitchDB(t)
	sc := newSidecar(t, "empty-base")
	require.Equal(t, http.StatusOK, reportHandshake(t, sc, reportBody(t, laneAnalyzerFile)).Code)
	w, resp := callAdmin(t, Patch, http.MethodPatch, sc.ID, `{"load_from_disk": false}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)

	cfg := daemon.Config(resp.Configuration)
	cfg.Listeners[0].Analyzer = &daemon.LaneAnalyzerConfig{}
	_, err := models.UpdateSidecarConfiguration(models.DB, switchOrgID, sc.ID, models.SidecarConfiguration(cfg))
	require.NoError(t, err)

	w, _ = callAdmin(t, Patch, http.MethodPatch, sc.ID, `{"log_level": "debug"}`)
	assert.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
}

// With no rule bound, a lane block that names no action is what the sidecar
// gets, and it refuses it.
func TestALaneAnalyzerWithNoActionAndNoRuleIsRefused(t *testing.T) {
	startSwitchDB(t)
	sc := newSidecar(t, "no-action")
	w, _ := callAdmin(t, Put, http.MethodPut, sc.ID, `{
		"analyzer": {"provider": "gemini", "model": "m"},
		"listeners": [{
			"name": "appdb", "protocol": "postgres", "listen": "0.0.0.0:15432", "upstream": "db:5432",
			"analyzer": {"max_calls": 50}
		}]
	}`)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code, "body: %s", w.Body)
	assert.Contains(t, w.Body.String(), "names no action")
}
