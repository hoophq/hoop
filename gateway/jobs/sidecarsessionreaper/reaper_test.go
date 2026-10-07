package sidecarsessionreaper

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite/pglitetest"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) { os.Exit(pglitetest.Main(m)) }

const reaperOrgID = "00000000-0000-0000-0000-0000000000f1"

func startReaperDB(t *testing.T) *models.Sidecar {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	inst := pglitetest.StartMigrated(t)
	require.NoError(t, models.InitDatabaseConnection(inst.DSN(), 1))
	require.NoError(t, models.DB.Exec(
		`INSERT INTO private.orgs (id, name) VALUES (?, 'sidecar-session-reaper-test')`, reaperOrgID).Error)
	sc := &models.Sidecar{OrgID: reaperOrgID, Name: "edge", KeyHash: models.HashAPIKey("hsc_reaper"), CreatedBy: "tests@hoop.dev"}
	require.NoError(t, models.CreateSidecar(models.DB, sc))
	setLastSeen(t, sc, time.Now())
	return sc
}

func setLastSeen(t *testing.T, sc *models.Sidecar, at time.Time) {
	t.Helper()
	require.NoError(t, models.DB.Exec(`UPDATE private.sidecars SET last_seen_at = ? WHERE id = ?`, at, sc.ID).Error)
}

func event(seq int64, sessionID string, kind audit.Kind, at time.Time, statement string) daemon.SessionEvent {
	return daemon.SessionEvent{Seq: seq, Event: audit.Event{
		Kind: kind, Timestamp: at, SessionID: session.ID(sessionID), Principal: "alice@example.com",
		Protocol: inspect.Postgres, Connection: "appdb", Statement: statement,
	}}
}

// openSession records a session that started a minute ago and ran a statement.
func openSession(t *testing.T, sc *models.Sidecar, sessionID string) string {
	t.Helper()
	start := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	_, err := services.ApplySidecarSessionEvents(models.DB, sc, []daemon.SessionEvent{
		event(1, sessionID, audit.KindSessionStart, start, ""),
		event(2, sessionID, audit.KindStatement, start.Add(time.Second), "SELECT 1"),
	})
	require.NoError(t, err)
	return services.SidecarSessionID(sc.ID, sessionID)
}

func setLastEvent(t *testing.T, id string, at time.Time) {
	t.Helper()
	require.NoError(t, models.DB.Exec(`UPDATE private.sessions
		SET metadata = jsonb_set(metadata, '{sidecar,last_event_at}', to_jsonb(?::text)) WHERE id = ?`,
		at.UTC().Format(time.RFC3339Nano), id).Error)
}

type reaperRow struct {
	Status   string
	EndedAt  *time.Time
	ReapedAt *string
}

func readRow(t *testing.T, id string) reaperRow {
	t.Helper()
	var row reaperRow
	require.NoError(t, models.DB.Raw(`SELECT status, ended_at, metadata->'sidecar'->>'reaped_at' AS reaped_at
		FROM private.sessions WHERE id = ?`, id).Take(&row).Error)
	return row
}

func reapNow(t *testing.T) int {
	t.Helper()
	n, err := services.ReapSidecarSessions(context.Background(), models.DB, time.Now().UTC())
	require.NoError(t, err)
	return n
}

func TestReapLeavesALiveSessionOpen(t *testing.T) {
	sc := startReaperDB(t)
	id := openSession(t, sc, "s-live")

	assert.Equal(t, 0, reapNow(t))
	assert.Equal(t, "open", readRow(t, id).Status)
}

func TestReapEndsTheSessionsOfAnUnseenSidecar(t *testing.T) {
	sc := startReaperDB(t)
	id := openSession(t, sc, "s-unseen")
	lastEvent := time.Now().UTC().Add(-30 * time.Second).Truncate(time.Microsecond)
	setLastEvent(t, id, lastEvent)
	setLastSeen(t, sc, time.Now().Add(-services.SidecarUnseenAfter-time.Minute))

	require.Equal(t, 1, reapNow(t))
	row := readRow(t, id)
	assert.Equal(t, "done", row.Status)
	require.NotNil(t, row.EndedAt)
	assert.True(t, lastEvent.Equal(row.EndedAt.UTC()), "ended at its last event: %v, want %v", row.EndedAt, lastEvent)
	assert.NotNil(t, row.ReapedAt)

	assert.Equal(t, 0, reapNow(t), "a reaped session is reaped once")

	var closed int64
	require.Eventually(t, func() bool {
		require.NoError(t, models.DB.Raw(`SELECT count(*) FROM private.events WHERE producer_event_id = ?`,
			id+":session.closed").Scan(&closed).Error)
		return closed == 1
	}, 10*time.Second, 50*time.Millisecond, "the reap publishes session.closed")
}

func TestReapEndsAnIdleSessionOfALiveSidecar(t *testing.T) {
	sc := startReaperDB(t)
	idle := openSession(t, sc, "s-idle")
	setLastEvent(t, idle, time.Now().Add(-services.SidecarSessionIdleAfter-time.Minute))
	quiet := openSession(t, sc, "s-quiet")
	setLastEvent(t, quiet, time.Now().Add(-time.Hour))

	require.Equal(t, 1, reapNow(t))
	assert.Equal(t, "done", readRow(t, idle).Status)
	assert.Equal(t, "open", readRow(t, quiet).Status, "an hour of quiet is an idle psql, not a dead session")
}

// The reaper guesses. What the sidecar sends later is still recorded, and its
// own session_end has the last word on the end.
func TestAReapedSessionTakesLateEvents(t *testing.T) {
	sc := startReaperDB(t)
	id := openSession(t, sc, "s-late")
	setLastSeen(t, sc, time.Now().Add(-services.SidecarUnseenAfter-time.Minute))
	require.Equal(t, 1, reapNow(t))

	end := time.Now().UTC().Truncate(time.Second)
	res, err := services.ApplySidecarSessionEvents(models.DB, sc, []daemon.SessionEvent{
		event(3, "s-late", audit.KindStatement, end.Add(-time.Second), "SELECT 2"),
		event(4, "s-late", audit.KindSessionEnd, end, ""),
	})
	require.NoError(t, err)
	assert.Equal(t, 2, res.Accepted)

	row := readRow(t, id)
	assert.Equal(t, "done", row.Status)
	require.NotNil(t, row.EndedAt)
	assert.True(t, end.Equal(row.EndedAt.UTC()), "ended at its session_end: %v, want %v", row.EndedAt, end)
	assert.Nil(t, row.ReapedAt, "the sidecar's end replaces the reap")

	_, err = services.ApplySidecarSessionEvents(models.DB, sc, []daemon.SessionEvent{
		event(5, "s-late", audit.KindStatement, end.Add(time.Second), "SELECT 3"),
	})
	var refused services.SidecarEventsRefused
	require.ErrorAs(t, err, &refused, "after its own end, the session refuses stray events")
}

func TestReapLeavesAnEndedSessionAlone(t *testing.T) {
	sc := startReaperDB(t)
	id := openSession(t, sc, "s-ended")
	_, err := services.ApplySidecarSessionEvents(models.DB, sc, []daemon.SessionEvent{
		event(3, "s-ended", audit.KindSessionEnd, time.Now().UTC(), ""),
	})
	require.NoError(t, err)
	before := readRow(t, id)
	setLastSeen(t, sc, time.Now().Add(-time.Hour))

	assert.Equal(t, 0, reapNow(t))
	assert.Equal(t, before, readRow(t, id))
}

func TestReapEndsTheSessionsOfADeletedSidecar(t *testing.T) {
	sc := startReaperDB(t)
	id := openSession(t, sc, "s-orphan")
	require.NoError(t, models.DB.Exec(`DELETE FROM private.sidecars WHERE id = ?`, sc.ID).Error)

	require.Equal(t, 1, reapNow(t))
	assert.Equal(t, "done", readRow(t, id).Status)
}

// A heartbeat between the list and the reap revives the session: the reap
// checks staleness again under the session's lock.
func TestReapSkipsASessionRevivedSinceItWasListed(t *testing.T) {
	sc := startReaperDB(t)
	id := openSession(t, sc, "s-revived")
	setLastSeen(t, sc, time.Now().Add(-time.Hour))
	now := time.Now().UTC()
	st := models.SidecarSessionStaleness{
		SidecarSeenBefore: now.Add(-services.SidecarUnseenAfter),
		EventBefore:       now.Add(-services.SidecarSessionIdleAfter),
	}
	stale, err := models.ListStaleSidecarSessions(context.Background(), models.DB, st, 10)
	require.NoError(t, err)
	require.Len(t, stale, 1)

	setLastSeen(t, sc, now)
	ok, err := models.ReapSidecarSession(models.DB, reaperOrgID, id, st, now)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, "open", readRow(t, id).Status)
}
