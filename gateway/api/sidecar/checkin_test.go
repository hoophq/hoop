package apisidecar

import (
	"net/http"
	"testing"

	"github.com/hoophq/hoop/gateway/liveevents"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOnlyTheFirstHandshakePublishesACheckIn(t *testing.T) {
	startSwitchDB(t)
	sc := newSidecar(t, "first-check-in")
	events, stop := liveevents.Subscribe(switchOrgID)
	defer stop()

	for range 3 {
		w := reportHandshake(t, sc, reportBody(t, switchFile))
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body)
	}

	var got []liveevents.Event
	for len(events) > 0 {
		if ev := <-events; ev.ID == sc.ID {
			got = append(got, ev)
		}
	}
	assert.Equal(t, []liveevents.Event{{Type: liveevents.SidecarCheckedIn, ID: sc.ID}}, got)
}
