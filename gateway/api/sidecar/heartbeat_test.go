package apisidecar

import (
	"net/http"
	"testing"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The interval is served and recorded only to a build that reports honoring
// the header. A build that ignores it keeps the minute, and its row must say
// so, or the sweeper and the reaper would wait a longer interval's worth of
// misses before reading it as gone.
func TestTheHeartbeatIntervalIsServedOnlyToABuildThatHonorsIt(t *testing.T) {
	startSwitchDB(t)
	w, created := postSidecar(t, "beat", `{"listeners": [
		{"name": "appdb", "protocol": "postgres", "listen": ":5432", "upstream": "db:5432"}]}`)
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body)
	sc, err := models.GetSidecarByNameOrID(models.DB, switchOrgID, created.ID)
	require.NoError(t, err)
	heartbeat := func() string {
		var out string
		require.NoError(t, models.DB.Raw(`SELECT COALESCE(heartbeat_seconds::text, 'null') FROM private.sidecars WHERE id = ?`, sc.ID).Scan(&out).Error)
		return out
	}

	w = handshake(t, sc, "protocol:postgres")
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	assert.Empty(t, w.Header().Values(daemon.HeartbeatSecondsHeader), "an older build is served no interval")
	assert.Equal(t, "null", heartbeat(), "and its row records none")

	w = handshake(t, sc, "protocol:postgres,"+daemon.CapabilityHeartbeatInterval)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	assert.Equal(t, "60", w.Header().Get(daemon.HeartbeatSecondsHeader), "a small fleet is told the default")
	assert.Equal(t, "60", heartbeat())

	w = handshake(t, sc, "protocol:postgres")
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	assert.Equal(t, "null", heartbeat(), "a downgraded build records none again")
}
