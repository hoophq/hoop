package apisidecar

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const (
	eventsMirrorOrgID = "00000000-0000-0000-0000-0000000000ed"
	eventsHooksOrgID  = "00000000-0000-0000-0000-0000000000ee"
)

// A listener name the connection name rule refuses gets a fallback mirror
// name; the session must name that mirror, not rebuild <sidecar>-<listener>.
func TestPostEventsFilesTheSessionUnderItsMirror(t *testing.T) {
	startEventsDB(t, eventsMirrorOrgID)
	enableSessionEvents(t, eventsMirrorOrgID)
	sc := &models.Sidecar{
		OrgID:     eventsMirrorOrgID,
		Name:      "edge",
		KeyHash:   models.HashAPIKey("hsc_edge_mirror"),
		CreatedBy: "tests@hoop.dev",
		Configuration: models.SidecarConfiguration{Listeners: []daemon.ListenerConfig{{
			Name: "app db", Protocol: "postgres",
		}}},
	}
	require.NoError(t, models.CreateSidecar(models.DB, sc))
	require.NoError(t, models.DB.Transaction(func(tx *gorm.DB) error {
		return services.SyncSidecarListenerConnectionsTx(tx, sc)
	}))
	mirror, err := models.GetSidecarMirror(models.DB, eventsMirrorOrgID, sc.ID, "app db")
	require.NoError(t, err)
	require.NotEqual(t, "edge-app db", mirror.Name, "the listener name must force the fallback name")

	events := fullSession("s-mirror")
	for i := range events {
		events[i].Event.Connection = "app db"
	}
	decodeEventsResponse(t, postEvents(sc, eventsBody(t, events...)))

	row := getSidecarSessionRow(t, eventsMirrorOrgID, services.SidecarSessionID(sc.ID, "s-mirror"))
	assert.Equal(t, mirror.Name, row.Connection)
}

// A held statement carries the gateway review id. The review's session then
// points at the sidecar session, and the sidecar session lists the review's.
func TestPostEventsLinksAHeldStatementToItsReview(t *testing.T) {
	startStatusTestDB(t)
	enableSessionEvents(t, statusTestOrgID)
	sc := seedStatusSidecar(t, "edge")
	other := seedStatusSidecar(t, "other")
	rev := seedStatusReview(t, sc, models.ReviewStatusApproved)
	foreign := seedStatusReview(t, other, models.ReviewStatusApproved)

	held := func(seq int64, kind audit.Kind, reviewID string) daemon.SessionEvent {
		return sessionEvent(seq, "s-held", int(seq), kind, func(e *audit.Event) {
			e.Statement = "DELETE FROM users WHERE id = 7"
			e.Metadata = map[string]string{analyzer.MetadataReviewID: reviewID}
		})
	}
	decodeEventsResponse(t, postEvents(sc, eventsBody(t,
		sessionEvent(1, "s-held", 0, audit.KindSessionStart, nil),
		held(2, audit.KindStatement, rev.ID),
		// Another sidecar's review: an id in an event is not trusted.
		held(3, audit.KindViolation, foreign.ID),
	)))
	sid := services.SidecarSessionID(sc.ID, "s-held")

	assert.Equal(t, sid, reviewSessionLink(t, rev.SessionID))
	assert.Empty(t, reviewSessionLink(t, foreign.SessionID))
	meta := sidecarMetadata(t, getSidecarSessionRow(t, statusTestOrgID, sid))
	assert.Equal(t, []any{rev.SessionID}, meta["review_sessions"])

	// The same review again, in a later batch, links once.
	decodeEventsResponse(t, postEvents(sc, eventsBody(t, held(4, audit.KindStatement, rev.ID))))
	meta = sidecarMetadata(t, getSidecarSessionRow(t, statusTestOrgID, sid))
	assert.Equal(t, []any{rev.SessionID}, meta["review_sessions"])
}

// reviewSessionLink is metadata.sidecar_session_id of a review's session.
func reviewSessionLink(t *testing.T, reviewSessionID string) string {
	t.Helper()
	var link *string
	require.NoError(t, models.DB.Raw(`SELECT metadata->>'sidecar_session_id' FROM private.sessions WHERE id = ?`,
		reviewSessionID).Scan(&link).Error)
	if link == nil {
		return ""
	}
	return *link
}

// A sidecar session publishes what an agent session does to event routing,
// once: on its start, and on its end with one event per denying rule.
func TestPostEventsPublishesTheSessionEvents(t *testing.T) {
	startEventsDB(t, eventsHooksOrgID)
	enableSessionEvents(t, eventsHooksOrgID)
	sc := seedEventsSidecar(t, eventsHooksOrgID, "edge")

	decodeEventsResponse(t, postEvents(sc, eventsBody(t, fullSession("s-hooks")...)))
	sid := services.SidecarSessionID(sc.ID, "s-hooks")

	want := []string{
		sid + ":session.started",
		sid + ":session.closed",
		sid + ":session.guardrail_violation:no-drop",
	}
	countPublished := func() int64 {
		var n int64
		require.NoError(t, models.DB.Raw(`SELECT count(*) FROM private.events
			WHERE org_id = ? AND producer_event_id IN ?`, eventsHooksOrgID, want).Scan(&n).Error)
		return n
	}
	require.Eventually(t, func() bool { return countPublished() == int64(len(want)) },
		10*time.Second, 50*time.Millisecond, "event routing rows for the session")

	// The denial points at its statement's row in the stream.
	row := getSidecarSessionRow(t, eventsHooksOrgID, sid)
	require.NotNil(t, row.GuardrailsInfo)
	var rails []models.SessionGuardRailsInfo
	require.NoError(t, json.Unmarshal([]byte(*row.GuardrailsInfo), &rails))
	require.Len(t, rails, 1)
	require.NotNil(t, rails[0].Elapsed)
	_, _, entries := readStream(t, eventsHooksOrgID, sid)
	var denied []string
	for _, e := range entries {
		if e.Elapsed == *rails[0].Elapsed && e.Kind == "i" {
			denied = append(denied, e.Text)
		}
	}
	assert.Equal(t, []string{"DROP TABLE users"}, denied)
}
