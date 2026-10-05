package apisidecar

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	apisession "github.com/hoophq/hoop/gateway/api/session"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The session routes of the gateway on a sidecar session. One organization
// per test, as in events_test.go.
const (
	eventsKillOrgID       = "00000000-0000-0000-0000-0000000000ed"
	eventsEndedOrgID      = "00000000-0000-0000-0000-0000000000ee"
	eventsVisibilityOrgID = "00000000-0000-0000-0000-0000000000ef"
	eventsMetadataOrgID   = "00000000-0000-0000-0000-0000000000f0"
)

// callSessionRoute runs handler as a user of orgID with groups.
func callSessionRoute(orgID, email string, groups []string, req *http.Request, sessionID string,
	handler gin.HandlerFunc) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	c.Params = gin.Params{{Key: "session_id", Value: sessionID}}
	c.Set(storagev2.ContextKey, storagev2.NewContext("subject-"+email, orgID).
		WithUserInfo("Test", email, "active", "", groups))
	handler(c)
	// gin holds a bare WriteHeader until the body or this call.
	c.Writer.WriteHeaderNow()
	return rec
}

// An open sidecar session, with its stream as the plane recorded it.
func openSidecarSession(t *testing.T, orgID string) (id, stream string) {
	t.Helper()
	startEventsDB(t, orgID)
	enableSessionEvents(t, orgID)
	sc := seedEventsSidecar(t, orgID, "edge")
	decodeEventsResponse(t, postEvents(sc, eventsBody(t, fullSession("s1")[:2]...)))
	id = services.SidecarSessionID(sc.ID, "s1")
	stream, _, _ = readStream(t, orgID, id)
	return id, stream
}

// Kill refuses a sidecar session before it calls the transport. Past that
// point the audit plugin replaces the recording with a placeholder.
func TestKillRefusesASidecarSession(t *testing.T) {
	id, before := openSidecarSession(t, eventsKillOrgID)
	kill := func(email string, groups []string) *httptest.ResponseRecorder {
		return callSessionRoute(eventsKillOrgID, email, groups,
			httptest.NewRequest(http.MethodPost, "/api/sessions/"+id+"/kill", nil), id, apisession.Kill)
	}

	rec := kill("admin@hoop.dev", []string{types.GroupAdmin})
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "sidecar")
	// Who cannot kill the session does not learn that it exists.
	for _, who := range []struct {
		email  string
		groups []string
	}{{"alice@example.com", nil}, {"auditor@hoop.dev", []string{types.GroupAuditor}}} {
		rec := kill(who.email, who.groups)
		assert.Equal(t, http.StatusNotFound, rec.Code, "%s: %s", who.email, rec.Body.String())
	}

	row := getSidecarSessionRow(t, eventsKillOrgID, id)
	assert.Equal(t, "open", row.Status)
	assert.Nil(t, row.EndedAt)
	after, _, _ := readStream(t, eventsKillOrgID, id)
	assert.Equal(t, before, after, "the kill changed the recording")
}

// A done session never grows: events past session_end are refused, and a
// resend of its own batch stays a duplicate.
func TestPostEventsRefusesEventsPastTheEnd(t *testing.T) {
	startEventsDB(t, eventsEndedOrgID)
	enableSessionEvents(t, eventsEndedOrgID)
	sc := seedEventsSidecar(t, eventsEndedOrgID, "edge")
	decodeEventsResponse(t, postEvents(sc, eventsBody(t, fullSession("s1")...)))
	id := services.SidecarSessionID(sc.ID, "s1")
	before, _, _ := readStream(t, eventsEndedOrgID, id)

	got := decodeEventsResponse(t, postEvents(sc, eventsBody(t, fullSession("s1")...)))
	assert.Equal(t, 0, got.Accepted)
	assert.Equal(t, 6, got.Duplicates)

	late := sessionEvent(7, "s1", 6, audit.KindStatement, func(e *audit.Event) {
		e.Statement = "SELECT 2"
		e.Allowed = true
	})
	rec := postEvents(sc, eventsBody(t, late))
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "has ended")

	after, _, _ := readStream(t, eventsEndedOrgID, id)
	assert.Equal(t, before, after, "an event past the end changed the recording")
	row := getSidecarSessionRow(t, eventsEndedOrgID, id)
	assert.EqualValues(t, 6, sidecarMetadata(t, row)["last_seq"])
}

// A sidecar session has no hoop owner, so only admins and auditors list and
// open it. The list hides an empty user_id from other users, but not NULL.
func TestSidecarSessionIsVisibleOnlyToAdminsAndAuditors(t *testing.T) {
	id, _ := openSidecarSession(t, eventsVisibilityOrgID)

	var emptyOwner bool
	require.NoError(t, models.DB.Raw(
		`SELECT user_id = '' FROM private.sessions WHERE id = ?`, id).Scan(&emptyOwner).Error)
	require.True(t, emptyOwner, "a sidecar session must store user_id '', not NULL")

	listed := func(userID string, adminOrAuditor bool) bool {
		opt := models.NewSessionOption()
		opt.Limit = 100
		list, err := models.ListSessions(eventsVisibilityOrgID, userID, adminOrAuditor, opt)
		require.NoError(t, err)
		for _, s := range list.Items {
			if s.ID == id {
				return true
			}
		}
		return false
	}
	assert.False(t, listed("subject-alice@example.com", false), "a user lists a sidecar session")
	assert.True(t, listed("subject-auditor@hoop.dev", true), "an auditor does not list a sidecar session")

	get := func(email string, groups []string) int {
		return callSessionRoute(eventsVisibilityOrgID, email, groups,
			httptest.NewRequest(http.MethodGet, "/api/sessions/"+id, nil), id, apisession.Get).Code
	}
	// The principal's email is not a hoop identity.
	assert.Equal(t, http.StatusForbidden, get("alice@example.com", nil))
	assert.Equal(t, http.StatusOK, get("auditor@hoop.dev", []string{types.GroupAuditor}))
}

// user_email of a sidecar session is a database principal, not a hoop user. A
// metadata patch matched it and wiped the seq that dedups resends.
func TestPatchMetadataLeavesASidecarSessionAlone(t *testing.T) {
	id, _ := openSidecarSession(t, eventsMetadataOrgID)
	patch := func(sessionID string) int {
		return callSessionRoute(eventsMetadataOrgID, "alice@example.com", nil,
			httptest.NewRequest(http.MethodPatch, "/api/sessions/"+sessionID+"/metadata",
				strings.NewReader(`{"metadata":{"reason":"x"}}`)), sessionID, apisession.PatchMetadata).Code
	}

	assert.Equal(t, http.StatusNotFound, patch(id))
	row := getSidecarSessionRow(t, eventsMetadataOrgID, id)
	assert.EqualValues(t, 2, sidecarMetadata(t, row)["last_seq"])
	assert.NotContains(t, row.Metadata, "reason")

	// A session from before origin existed has origin NULL, and stays patchable.
	legacy := uuid.NewString()
	require.NoError(t, models.DB.Exec(`
	INSERT INTO private.sessions (id, org_id, connection, connection_type, verb, status, user_id, user_email)
	VALUES (?, ?, 'pg', 'database', 'connect', 'done', 'subject-alice@example.com', 'alice@example.com')`,
		legacy, eventsMetadataOrgID).Error)
	assert.Equal(t, http.StatusNoContent, patch(legacy))
	var meta string
	require.NoError(t, models.DB.Raw(`SELECT metadata::text FROM private.sessions WHERE id = ?`, legacy).Scan(&meta).Error)
	assert.JSONEq(t, `{"reason":"x"}`, meta)
}
