package apisidecar

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/liveevents"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The token middleware loads the sidecar without its runtime columns, so the
// handler cannot tell a first check-in from the row it gets.
func handshakeWithToken(t *testing.T, sc *models.Sidecar, body string) int {
	t.Helper()
	row, err := models.GetSidecarByKeyHash(models.DB, sc.KeyHash)
	require.NoError(t, err)
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/sidecars/handshake", bytes.NewReader([]byte(body)))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("sidecar-auth", row)
	Handshake(c)
	return w.Code
}

func TestOnlyTheFirstHandshakePublishesACheckIn(t *testing.T) {
	startSwitchDB(t)
	sc := newSidecar(t, "first-check-in")
	events, stop := liveevents.Subscribe(switchOrgID)
	defer stop()

	for range 3 {
		require.Equal(t, http.StatusOK, handshakeWithToken(t, sc, reportBody(t, switchFile)))
	}

	var got []liveevents.Event
	for len(events) > 0 {
		if ev := <-events; ev.ID == sc.ID {
			got = append(got, ev)
		}
	}
	assert.Equal(t, []liveevents.Event{{Type: liveevents.SidecarCheckedIn, ID: sc.ID}}, got)
}
