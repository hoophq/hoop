package apisidecar

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	modelsbootstrap "github.com/hoophq/hoop/gateway/models/bootstrap"
	"github.com/hoophq/hoop/gateway/pglite"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/session"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// One organization per test, so a flag one test sets is not read by another.
const (
	eventsFlagOffOrgID   = "00000000-0000-0000-0000-0000000000e1"
	eventsLimitsOrgID    = "00000000-0000-0000-0000-0000000000e2"
	eventsMalformedOrgID = "00000000-0000-0000-0000-0000000000e3"
	eventsFullOrgID      = "00000000-0000-0000-0000-0000000000e4"
	eventsResendOrgID    = "00000000-0000-0000-0000-0000000000e5"
	eventsNoStartOrgID   = "00000000-0000-0000-0000-0000000000e6"
	eventsProtocolOrgID  = "00000000-0000-0000-0000-0000000000e7"
	eventsHandshakeOrgID = "00000000-0000-0000-0000-0000000000e8"
	eventsFlagOffDBOrgID = "00000000-0000-0000-0000-0000000000e9"
	eventsColumnsOrgID   = "00000000-0000-0000-0000-0000000000ea"
)

// eventsT0 is when every test session starts. Whole seconds, so the elapsed
// time of each stream entry is exact.
var eventsT0 = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

// enableSessionEvents turns the flag on for one organization until the test
// ends.
func enableSessionEvents(t *testing.T, orgID string) {
	t.Helper()
	featureflag.Set(orgID, services.SidecarSessionEventsFlag, true)
	t.Cleanup(func() { featureflag.SetAll(orgID, map[string]bool{}) })
}

// memorySidecar is a sidecar the token middleware resolved. The tests that
// use it stop before the handler reads the database.
func memorySidecar(orgID string) *models.Sidecar {
	return &models.Sidecar{ID: uuid.NewString(), OrgID: orgID, Name: "edge"}
}

// startEventsDB boots the embedded database the way the gateway does, with
// one organization in it.
func startEventsDB(t *testing.T, orgID string) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	ctx := context.Background()
	inst, err := pglite.Start(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := inst.Close(ctx); err != nil {
			t.Errorf("close embedded database: %v", err)
		}
	})
	require.NoError(t, modelsbootstrap.MigrateDB(inst.MigrateDSN(), ""))
	// The embedded backend serves one session at a time.
	require.NoError(t, models.InitDatabaseConnection(inst.DSN(), 1))
	require.NoError(t, models.DB.Exec(
		`INSERT INTO private.orgs (id, name) VALUES (?, 'sidecar-session-events-test')`, orgID).Error)
}

func seedEventsSidecar(t *testing.T, orgID, name string) *models.Sidecar {
	t.Helper()
	sc := &models.Sidecar{
		OrgID:     orgID,
		Name:      name,
		KeyHash:   models.HashAPIKey("hsc_" + name),
		CreatedBy: "tests@hoop.dev",
	}
	require.NoError(t, models.CreateSidecar(models.DB, sc))
	return sc
}

func eventsBody(t *testing.T, events ...daemon.SessionEvent) []byte {
	t.Helper()
	raw, err := json.Marshal(daemon.SessionEventsRequest{Events: events})
	require.NoError(t, err)
	return raw
}

// callPostEvents runs the handler on req. A nil sidecar is a request that
// skipped the token middleware.
func callPostEvents(sc *models.Sidecar, req *http.Request) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	if sc != nil {
		c.Set("sidecar-auth", sc)
	}
	PostEvents(c)
	return rec
}

func postEvents(sc *models.Sidecar, body []byte) *httptest.ResponseRecorder {
	return callPostEvents(sc, httptest.NewRequest(http.MethodPost, "/api/sidecars/events", bytes.NewReader(body)))
}

func decodeEventsResponse(t *testing.T, rec *httptest.ResponseRecorder) openapi.SidecarSessionEventsResponse {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var got openapi.SidecarSessionEventsResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	return got
}

// sessionEvent is one event of a postgres session on listener appdb, sec
// seconds after eventsT0.
func sessionEvent(seq int64, sessionID string, sec int, kind audit.Kind, set func(*audit.Event)) daemon.SessionEvent {
	ev := audit.Event{
		Kind:       kind,
		Timestamp:  eventsT0.Add(time.Duration(sec) * time.Second),
		SessionID:  session.ID(sessionID),
		Principal:  "alice@example.com",
		Protocol:   inspect.Postgres,
		Connection: "appdb",
	}
	if set != nil {
		set(&ev)
	}
	return daemon.SessionEvent{Seq: seq, Event: ev}
}

// fullSession is one session with every kind that writes: start, a
// statement, a denied statement, a masked answer, an error and the end.
func fullSession(sessionID string) []daemon.SessionEvent {
	return []daemon.SessionEvent{
		sessionEvent(1, sessionID, 0, audit.KindSessionStart, nil),
		sessionEvent(2, sessionID, 1, audit.KindStatement, func(e *audit.Event) {
			e.Statement = "SELECT * FROM users"
			e.Allowed = true
		}),
		sessionEvent(3, sessionID, 2, audit.KindViolation, func(e *audit.Event) {
			e.Statement = "DROP TABLE users"
			e.Rule = "no-drop"
			e.Message = "drops need a review"
			e.Direction = inspect.FromClient
		}),
		sessionEvent(4, sessionID, 3, audit.KindMasked, func(e *audit.Event) {
			e.MaskedEntities = []string{"ssn", "email"}
			e.MaskedCount = 3
			e.Direction = inspect.FromServer
		}),
		sessionEvent(5, sessionID, 4, audit.KindError, func(e *audit.Event) {
			e.Error = "upstream closed the connection"
		}),
		sessionEvent(6, sessionID, 5, audit.KindSessionEnd, func(e *audit.Event) {
			e.StatementCount = 2
			e.DeniedCount = 1
			e.Duration = 5 * time.Second
		}),
	}
}

// sidecarSessionRow is the private.sessions row of a sidecar session, with
// the jsonb columns as text.
type sidecarSessionRow struct {
	Connection        string
	ConnectionType    string
	ConnectionSubtype string
	Verb              string
	Status            string
	RecordingFormat   *string
	IdentityType      string
	Origin            string
	UserName          string
	UserEmail         string
	Metadata          string
	Metrics           *string
	GuardrailsInfo    *string
	BlobStreamID      *string
	CreatedAt         time.Time
	EndedAt           *time.Time
}

func getSidecarSessionRow(t *testing.T, orgID, id string) sidecarSessionRow {
	t.Helper()
	var row sidecarSessionRow
	require.NoError(t, models.DB.Raw(`
	SELECT connection, connection_type, connection_subtype, verb, status, recording_format,
		identity_type, origin, user_name, user_email, metadata::text AS metadata,
		metrics::text AS metrics, guardrails_info::text AS guardrails_info, blob_stream_id,
		created_at, ended_at
	FROM private.sessions WHERE org_id = ? AND id = ?`, orgID, id).Take(&row).Error)
	return row
}

func countSessions(t *testing.T, orgID, id string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, models.DB.Raw(
		`SELECT count(*) FROM private.sessions WHERE org_id = ? AND id = ?`, orgID, id).Scan(&n).Error)
	return n
}

// sidecarMetadata is metadata.sidecar of a session row.
func sidecarMetadata(t *testing.T, row sidecarSessionRow) map[string]any {
	t.Helper()
	var meta map[string]any
	require.NoError(t, json.Unmarshal([]byte(row.Metadata), &meta))
	sc, ok := meta["sidecar"].(map[string]any)
	require.True(t, ok, "metadata has no sidecar object: %s", row.Metadata)
	return sc
}

// streamEntry is one [elapsed, type, base64] entry, decoded.
type streamEntry struct {
	Elapsed float64
	Kind    string
	Text    string
}

// readStream reads the stream blob of a session: its raw text, its format
// and its decoded entries.
func readStream(t *testing.T, orgID, sessionID string) (string, *string, []streamEntry) {
	t.Helper()
	var blob struct {
		BlobStream string
		Format     *string
	}
	require.NoError(t, models.DB.Raw(
		`SELECT blob_stream::text AS blob_stream, format FROM private.blobs WHERE org_id = ? AND id = ?`,
		orgID, models.SessionStreamBlobID(sessionID)).Take(&blob).Error)
	var raw [][]any
	require.NoError(t, json.Unmarshal([]byte(blob.BlobStream), &raw))
	entries := make([]streamEntry, 0, len(raw))
	for _, e := range raw {
		require.Len(t, e, 3, "entry %v is not [elapsed, type, base64]", e)
		elapsed, ok := e[0].(float64)
		require.True(t, ok, "elapsed %v is not a number", e[0])
		kind, ok := e[1].(string)
		require.True(t, ok, "type %v is not a string", e[1])
		b64, ok := e[2].(string)
		require.True(t, ok, "payload %v is not a string", e[2])
		text, err := base64.StdEncoding.DecodeString(b64)
		require.NoError(t, err)
		entries = append(entries, streamEntry{Elapsed: elapsed, Kind: kind, Text: string(text)})
	}
	return blob.BlobStream, blob.Format, entries
}

// streamSize is the size the audit plugin counts for entries: the bytes of
// each encoded entry.
func streamSize(t *testing.T, entries []streamEntry) int64 {
	t.Helper()
	var n int64
	for _, e := range entries {
		raw, err := json.Marshal([]any{e.Elapsed, e.Kind, base64.StdEncoding.EncodeToString([]byte(e.Text))})
		require.NoError(t, err)
		n += int64(len(raw))
	}
	return n
}

type maskedRow struct {
	InfoType       string
	CountMasked    int64
	SessionEndedAt *time.Time
}

func readMaskedMetrics(t *testing.T, sessionID string) []maskedRow {
	t.Helper()
	var rows []maskedRow
	require.NoError(t, models.DB.Raw(
		`SELECT info_type, count_masked, session_ended_at FROM private.session_metrics
		WHERE session_id = ? ORDER BY info_type`, sessionID).Scan(&rows).Error)
	return rows
}

func TestPostEventsRefusesARequestThatSkippedTheMiddleware(t *testing.T) {
	rec := postEvents(nil, []byte(`{"events":[]}`))
	assert.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())
}

// The flag is read on every request, so a sidecar stops when it goes off. The
// body is not read: a 412 must not depend on what the sidecar sent.
func TestPostEventsAnswers412WhileTheFlagIsOff(t *testing.T) {
	sc := memorySidecar(eventsFlagOffOrgID)
	rec := postEvents(sc, []byte(`not json`))
	assert.Equal(t, http.StatusPreconditionFailed, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), services.SidecarSessionEventsFlag)

	enableSessionEvents(t, eventsFlagOffOrgID)
	rec = postEvents(sc, []byte(`not json`))
	assert.Equal(t, http.StatusBadRequest, rec.Code, "with the flag on the body is read: %s", rec.Body)
}

// The limits are refused before the database is read: no test here boots it.
func TestPostEventsRefusesABatchOverTheLimits(t *testing.T) {
	enableSessionEvents(t, eventsLimitsOrgID)
	sc := memorySidecar(eventsLimitsOrgID)

	t.Run("more events than a batch carries", func(t *testing.T) {
		events := make([]daemon.SessionEvent, daemon.MaxSessionEventsBatch+1)
		for i := range events {
			events[i] = sessionEvent(int64(i+1), "s-count", 0, audit.KindActivity, nil)
		}
		body := eventsBody(t, events...)
		require.Less(t, len(body), daemon.MaxSessionEventsBatchBytes, "the count alone must be over")
		rec := postEvents(sc, body)
		assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, rec.Body.String())
	})

	t.Run("a batch at the count limit is not refused for its count", func(t *testing.T) {
		events := make([]daemon.SessionEvent, daemon.MaxSessionEventsBatch)
		for i := range events {
			// seq 0 makes the batch fail after the count check, before the
			// database.
			events[i] = sessionEvent(0, "s-count", 0, audit.KindActivity, nil)
		}
		rec := postEvents(sc, eventsBody(t, events...))
		assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	})

	t.Run("a body over the byte limit without a length", func(t *testing.T) {
		body := eventsBody(t, sessionEvent(1, "s-bytes", 0, audit.KindStatement, func(e *audit.Event) {
			e.Statement = strings.Repeat("x", daemon.MaxSessionEventsBatchBytes)
		}))
		require.Greater(t, len(body), daemon.MaxSessionEventsBatchBytes)
		req := httptest.NewRequest(http.MethodPost, "/api/sidecars/events", bytes.NewReader(body))
		// A chunked body: the handler must count the bytes it reads.
		req.ContentLength = -1
		rec := callPostEvents(sc, req)
		assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, rec.Body.String())
	})

	t.Run("a body at the byte limit is not refused for its size", func(t *testing.T) {
		// The sidecar fills a batch up to the limit, so the limit itself must
		// pass. seq 0 stops the batch after the size checks.
		event := sessionEvent(0, "s-bytes", 0, audit.KindStatement, func(e *audit.Event) { e.Statement = "x" })
		event.Event.Statement += strings.Repeat("x", daemon.MaxSessionEventsBatchBytes-len(eventsBody(t, event)))
		body := eventsBody(t, event)
		require.Equal(t, daemon.MaxSessionEventsBatchBytes, len(body))
		rec := postEvents(sc, body)
		assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	})

	t.Run("a content length over the byte limit", func(t *testing.T) {
		body := eventsBody(t, sessionEvent(1, "s-length", 0, audit.KindStatement, nil))
		req := httptest.NewRequest(http.MethodPost, "/api/sidecars/events", bytes.NewReader(body))
		req.ContentLength = daemon.MaxSessionEventsBatchBytes + 1
		rec := callPostEvents(sc, req)
		assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, rec.Body.String())
	})
}

// A malformed batch is a 400, so the sidecar does not resend it. None of
// these reach the database.
func TestPostEventsRefusesAMalformedBatch(t *testing.T) {
	enableSessionEvents(t, eventsMalformedOrgID)
	sc := memorySidecar(eventsMalformedOrgID)

	for name, body := range map[string][]byte{
		"invalid json":  []byte(`{"events":[`),
		"wrong shape":   []byte(`{"events":{"seq":1}}`),
		"seq 0":         eventsBody(t, sessionEvent(0, "s-1", 0, audit.KindSessionStart, nil)),
		"negative seq":  eventsBody(t, sessionEvent(-1, "s-1", 0, audit.KindSessionStart, nil)),
		"no session_id": eventsBody(t, sessionEvent(1, "", 0, audit.KindSessionStart, nil)),
		"session_id over 256 bytes": eventsBody(t,
			sessionEvent(1, strings.Repeat("s", 257), 0, audit.KindSessionStart, nil)),
		"one bad event in a good batch": eventsBody(t,
			sessionEvent(1, "s-1", 0, audit.KindSessionStart, nil),
			sessionEvent(2, "", 1, audit.KindStatement, nil)),
	} {
		t.Run(name, func(t *testing.T) {
			rec := postEvents(sc, body)
			assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		})
	}
}

func TestPostEventsRecordsAFullSession(t *testing.T) {
	startEventsDB(t, eventsFullOrgID)
	enableSessionEvents(t, eventsFullOrgID)
	sc := seedEventsSidecar(t, eventsFullOrgID, "edge-full")

	rec := postEvents(sc, eventsBody(t, fullSession("s-full")...))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{"accepted": 6, "duplicates": 0}`, rec.Body.String())

	id := services.SidecarSessionID(sc.ID, "s-full")
	row := getSidecarSessionRow(t, eventsFullOrgID, id)
	assert.Equal(t, "edge-full-appdb", row.Connection)
	assert.Equal(t, "database", row.ConnectionType)
	assert.Equal(t, "postgres", row.ConnectionSubtype)
	assert.Equal(t, "connect", row.Verb)
	assert.Equal(t, "done", row.Status)
	require.NotNil(t, row.RecordingFormat)
	assert.Equal(t, "raw", *row.RecordingFormat)
	assert.Equal(t, "sidecar", row.IdentityType)
	assert.Equal(t, "sidecar", row.Origin)
	assert.Equal(t, "alice@example.com", row.UserEmail)
	assert.Empty(t, row.UserName)
	assert.Equal(t, eventsT0, row.CreatedAt.UTC())
	require.NotNil(t, row.EndedAt)
	assert.Equal(t, eventsT0.Add(5*time.Second), row.EndedAt.UTC())
	require.NotNil(t, row.BlobStreamID)
	assert.Equal(t, models.SessionStreamBlobID(id), *row.BlobStreamID)

	meta := sidecarMetadata(t, row)
	assert.Equal(t, sc.ID, meta["id"])
	assert.Equal(t, "edge-full", meta["name"])
	assert.Equal(t, "appdb", meta["listener"])
	assert.Equal(t, "s-full", meta["session_id"])
	assert.EqualValues(t, 6, meta["last_seq"])
	assert.EqualValues(t, 2, meta["statement_count"])
	assert.EqualValues(t, 1, meta["denied_count"])

	require.NotNil(t, row.GuardrailsInfo)
	var guardrails []models.SessionGuardRailsInfo
	require.NoError(t, json.Unmarshal([]byte(*row.GuardrailsInfo), &guardrails))
	require.Len(t, guardrails, 1)
	assert.Equal(t, "no-drop", guardrails[0].RuleName)
	assert.Equal(t, "sidecar", guardrails[0].Rule.Type)
	assert.Equal(t, "input", guardrails[0].Direction)
	assert.NotNil(t, guardrails[0].MatchedWords, "the session page reads a list, not null")
	assert.Empty(t, guardrails[0].MatchedWords)
	assert.Equal(t, "drops need a review", guardrails[0].Message)

	raw, format, entries := readStream(t, eventsFullOrgID, id)
	assert.Nil(t, format, "statement text is not wire-proto")
	require.Len(t, entries, 4, raw)
	assert.Equal(t, streamEntry{1, "i", "SELECT * FROM users"}, entries[0])
	assert.Equal(t, streamEntry{2, "i", "DROP TABLE users"}, entries[1])
	// A microsecond after the statement, so the raw view keys the two apart.
	assert.Equal(t, 2.000001, entries[2].Elapsed)
	assert.Equal(t, "e", entries[2].Kind)
	assert.Contains(t, entries[2].Text, "no-drop")
	assert.Contains(t, entries[2].Text, "drops need a review")
	assert.Equal(t, streamEntry{4, "e", "upstream closed the connection"}, entries[3])

	require.NotNil(t, row.Metrics)
	var metrics map[string]any
	require.NoError(t, json.Unmarshal([]byte(*row.Metrics), &metrics))
	assert.EqualValues(t, streamSize(t, entries), metrics["event_size"])
	assert.Equal(t, false, metrics["truncated"])
	dm, ok := metrics["data_masking"].(map[string]any)
	require.True(t, ok, "metrics has no data_masking: %s", *row.Metrics)
	assert.EqualValues(t, 3, dm["total_redact_count"])
	assert.Equal(t, map[string]any{"email+ssn": float64(3)}, dm["info_types"],
		"an event naming several entities is filed under all of them joined")

	masked := readMaskedMetrics(t, id)
	require.Len(t, masked, 1)
	assert.Equal(t, "email+ssn", masked[0].InfoType)
	assert.EqualValues(t, 3, masked[0].CountMasked)
	require.NotNil(t, masked[0].SessionEndedAt, "the end of the session reaches its metrics")
	assert.Equal(t, eventsT0.Add(5*time.Second), masked[0].SessionEndedAt.UTC())
}

// A 5xx makes the sidecar resend the same events with the same seq. The
// resend answers 200 and writes nothing.
func TestPostEventsResendIsIdempotent(t *testing.T) {
	startEventsDB(t, eventsResendOrgID)
	enableSessionEvents(t, eventsResendOrgID)
	sc := seedEventsSidecar(t, eventsResendOrgID, "edge-resend")
	body := eventsBody(t, fullSession("s-resend")...)

	got := decodeEventsResponse(t, postEvents(sc, body))
	require.Equal(t, 6, got.Accepted)
	id := services.SidecarSessionID(sc.ID, "s-resend")
	before := getSidecarSessionRow(t, eventsResendOrgID, id)
	streamBefore, _, _ := readStream(t, eventsResendOrgID, id)

	got = decodeEventsResponse(t, postEvents(sc, body))
	assert.Equal(t, 0, got.Accepted)
	assert.Equal(t, 6, got.Duplicates)

	after := getSidecarSessionRow(t, eventsResendOrgID, id)
	streamAfter, _, _ := readStream(t, eventsResendOrgID, id)
	assert.Equal(t, streamBefore, streamAfter, "the stream is unchanged")
	assert.Equal(t, before, after, "the session row is unchanged")
	masked := readMaskedMetrics(t, id)
	require.Len(t, masked, 1)
	assert.EqualValues(t, 3, masked[0].CountMasked, "the masked count is not added twice")

	// A batch that overlaps the last one applies only the events after it.
	got = decodeEventsResponse(t, postEvents(sc, eventsBody(t,
		sessionEvent(6, "s-resend", 5, audit.KindSessionEnd, nil),
		sessionEvent(7, "s-resend", 6, audit.KindError, func(e *audit.Event) { e.Error = "late" }),
	)))
	assert.Equal(t, 1, got.Accepted)
	assert.Equal(t, 1, got.Duplicates)
	_, _, entries := readStream(t, eventsResendOrgID, id)
	require.Len(t, entries, 5)
	assert.Equal(t, streamEntry{6, "e", "late"}, entries[4])
	assert.EqualValues(t, 7, sidecarMetadata(t, getSidecarSessionRow(t, eventsResendOrgID, id))["last_seq"])
}

// The gateway can miss a session_start: the sidecar dropped it, or it went to
// a gateway with the flag off. The first event it receives creates the
// session.
func TestPostEventsCreatesASessionFromAStatement(t *testing.T) {
	startEventsDB(t, eventsNoStartOrgID)
	enableSessionEvents(t, eventsNoStartOrgID)
	sc := seedEventsSidecar(t, eventsNoStartOrgID, "edge-nostart")
	asRole := func(e *audit.Event) { e.Principal = "app_rw" }

	// seq 1 and 2 were dropped: a gap is allowed.
	got := decodeEventsResponse(t, postEvents(sc, eventsBody(t,
		sessionEvent(3, "s-late", 10, audit.KindStatement, func(e *audit.Event) {
			asRole(e)
			e.Statement = "SELECT 1"
		}),
		sessionEvent(4, "s-late", 11, audit.KindStatement, func(e *audit.Event) {
			asRole(e)
			e.Statement = "SELECT 2"
		}),
	)))
	assert.Equal(t, 2, got.Accepted)

	id := services.SidecarSessionID(sc.ID, "s-late")
	row := getSidecarSessionRow(t, eventsNoStartOrgID, id)
	assert.Equal(t, "edge-nostart-appdb", row.Connection)
	assert.Equal(t, "open", row.Status)
	assert.Nil(t, row.EndedAt)
	assert.Equal(t, "app_rw", row.UserName, "a principal without @ is a name")
	assert.Empty(t, row.UserEmail)
	assert.Equal(t, eventsT0.Add(10*time.Second), row.CreatedAt.UTC(),
		"the session starts at its first event the gateway received")
	assert.EqualValues(t, 4, sidecarMetadata(t, row)["last_seq"])
	_, _, entries := readStream(t, eventsNoStartOrgID, id)
	assert.Equal(t, []streamEntry{{0, "i", "SELECT 1"}, {1, "i", "SELECT 2"}}, entries)

	// The next batch counts its elapsed time from the stored start.
	got = decodeEventsResponse(t, postEvents(sc, eventsBody(t,
		sessionEvent(5, "s-late", 20, audit.KindStatement, func(e *audit.Event) {
			asRole(e)
			e.Statement = "SELECT 3"
		}),
		sessionEvent(6, "s-late", 21, audit.KindSessionEnd, asRole),
	)))
	assert.Equal(t, 2, got.Accepted)
	row = getSidecarSessionRow(t, eventsNoStartOrgID, id)
	assert.Equal(t, "done", row.Status)
	_, _, entries = readStream(t, eventsNoStartOrgID, id)
	require.Len(t, entries, 3)
	assert.Equal(t, streamEntry{10, "i", "SELECT 3"}, entries[2])

	// The same session id from another sidecar is another session.
	other := seedEventsSidecar(t, eventsNoStartOrgID, "edge-other")
	decodeEventsResponse(t, postEvents(other, eventsBody(t,
		sessionEvent(1, "s-late", 0, audit.KindSessionStart, nil))))
	otherID := services.SidecarSessionID(other.ID, "s-late")
	require.NotEqual(t, id, otherID)
	assert.Equal(t, "edge-other-appdb", getSidecarSessionRow(t, eventsNoStartOrgID, otherID).Connection)
	assert.Equal(t, "done", getSidecarSessionRow(t, eventsNoStartOrgID, id).Status)
}

// A session the gateway can never record answers 422, so the sidecar does not
// resend. The other sessions of the batch are still applied.
func TestPostEventsRefusesAnUnknownProtocolAndAppliesTheRest(t *testing.T) {
	startEventsDB(t, eventsProtocolOrgID)
	enableSessionEvents(t, eventsProtocolOrgID)
	sc := seedEventsSidecar(t, eventsProtocolOrgID, "edge-proto")
	redis := func(e *audit.Event) { e.Protocol = inspect.Protocol("redis") }

	rec := postEvents(sc, eventsBody(t,
		sessionEvent(1, "s-redis", 0, audit.KindSessionStart, redis),
		sessionEvent(1, "s-good", 0, audit.KindSessionStart, nil),
		sessionEvent(2, "s-redis", 1, audit.KindStatement, redis),
		sessionEvent(2, "s-good", 1, audit.KindStatement, func(e *audit.Event) { e.Statement = "SELECT 1" }),
	))
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "redis")

	assert.Zero(t, countSessions(t, eventsProtocolOrgID, services.SidecarSessionID(sc.ID, "s-redis")))
	goodID := services.SidecarSessionID(sc.ID, "s-good")
	row := getSidecarSessionRow(t, eventsProtocolOrgID, goodID)
	assert.Equal(t, "open", row.Status)
	assert.EqualValues(t, 2, sidecarMetadata(t, row)["last_seq"])
	_, _, entries := readStream(t, eventsProtocolOrgID, goodID)
	assert.Equal(t, []streamEntry{{1, "i", "SELECT 1"}}, entries)
}

// With the flag off the endpoint writes nothing, even with a database.
func TestPostEventsWritesNothingWhileTheFlagIsOff(t *testing.T) {
	startEventsDB(t, eventsFlagOffDBOrgID)
	sc := seedEventsSidecar(t, eventsFlagOffDBOrgID, "edge-off")

	rec := postEvents(sc, eventsBody(t, fullSession("s-off")...))
	require.Equal(t, http.StatusPreconditionFailed, rec.Code, rec.Body.String())
	assert.Zero(t, countSessions(t, eventsFlagOffDBOrgID, services.SidecarSessionID(sc.ID, "s-off")))
}

// The handshake offers the endpoint only while the flag is on, on both of its
// 200 answers, so the sidecar follows the flag within a heartbeat.
func TestHandshakeOffersSessionEventsOnlyWithTheFlag(t *testing.T) {
	startEventsDB(t, eventsHandshakeOrgID)
	sc := seedEventsSidecar(t, eventsHandshakeOrgID, "edge-hs")
	cfg, err := services.ParseSidecarConfiguration(json.RawMessage(listenerDoc(5432, "")))
	require.NoError(t, err)
	served, err := models.UpdateSidecarConfiguration(models.DB, eventsHandshakeOrgID, sc.ID, models.SidecarConfiguration(cfg))
	require.NoError(t, err)
	// The handshake reads the sidecar the token resolved, not the row.
	on := true
	disk := *served
	disk.Configuration = models.SidecarConfiguration(daemon.Config{LoadFromDisk: &on})
	empty := *served
	empty.Configuration = models.SidecarConfiguration{}

	answers := map[string]*models.Sidecar{"control plane": served, "load from disk": &disk}
	t.Cleanup(func() { featureflag.SetAll(eventsHandshakeOrgID, map[string]bool{}) })
	// Off, on, then off again: the header follows the flag both ways.
	for _, flag := range []bool{false, true, false} {
		featureflag.Set(eventsHandshakeOrgID, services.SidecarSessionEventsFlag, flag)
		for name, who := range answers {
			w := handshake(t, who, daemon.CapabilitySessionEvents)
			require.Equal(t, http.StatusOK, w.Code, "%s: %s", name, w.Body)
			if flag {
				assert.Equal(t, "true", w.Header().Get(daemon.SessionEventsHeader), "%s with the flag on", name)
			} else {
				assert.Empty(t, w.Header().Values(daemon.SessionEventsHeader), "%s with the flag off", name)
			}
		}
	}

	// A sidecar that cannot run is not offered the endpoint.
	featureflag.Set(eventsHandshakeOrgID, services.SidecarSessionEventsFlag, true)
	w := handshake(t, &empty, daemon.CapabilitySessionEvents)
	require.Equal(t, http.StatusPreconditionFailed, w.Code, w.Body.String())
	assert.Empty(t, w.Header().Values(daemon.SessionEventsHeader))
}

// A string a column refuses would fail the write on every resend, so the
// sidecar would resend the batch forever. The gateway fits it or refuses it.
func TestPostEventsFitsTheSidecarStringsToTheColumns(t *testing.T) {
	startEventsDB(t, eventsColumnsOrgID)
	enableSessionEvents(t, eventsColumnsOrgID)
	sc := seedEventsSidecar(t, eventsColumnsOrgID, "edge-cols")
	longEmail := "alice@" + strings.Repeat("x", 300)
	withNUL := func(e *audit.Event) {
		e.Connection = "app\x00db"
		e.Principal = "bob\x00@example.com"
	}
	as := func(p inspect.Protocol) func(*audit.Event) {
		return func(e *audit.Event) { e.Protocol = p }
	}

	got := decodeEventsResponse(t, postEvents(sc, eventsBody(t,
		sessionEvent(1, "s-long", 0, audit.KindSessionStart, func(e *audit.Event) { e.Principal = longEmail }),
		sessionEvent(1, "s-nul", 0, audit.KindSessionStart, withNUL),
		sessionEvent(2, "s-nul", 1, audit.KindViolation, func(e *audit.Event) {
			withNUL(e)
			e.Statement = "DROP\x00 TABLE users"
			e.Rule = "no\x00drop"
			e.Message = "deny\x00ed"
			e.Direction = inspect.FromServer
		}),
		sessionEvent(3, "s-nul", 2, audit.KindMasked, func(e *audit.Event) {
			withNUL(e)
			e.MaskedEntities = []string{"em\x00ail", "email"}
			e.MaskedCount = 2
		}),
		sessionEvent(1, "s-clickhouse", 0, audit.KindSessionStart, as("clickhouse")),
		sessionEvent(1, "s-http", 0, audit.KindSessionStart, as(inspect.HTTP)),
		sessionEvent(1, "s-ssh", 0, audit.KindSessionStart, as("ssh")),
	)))
	assert.Equal(t, 7, got.Accepted)

	long := getSidecarSessionRow(t, eventsColumnsOrgID, services.SidecarSessionID(sc.ID, "s-long"))
	assert.Equal(t, longEmail[:255], long.UserEmail, "the column keeps what fits")
	assert.Equal(t, longEmail, sidecarMetadata(t, long)["principal"], "the metadata keeps all of it")

	nulID := services.SidecarSessionID(sc.ID, "s-nul")
	nul := getSidecarSessionRow(t, eventsColumnsOrgID, nulID)
	assert.Equal(t, "edge-cols-appdb", nul.Connection)
	assert.Equal(t, "bob@example.com", nul.UserEmail)
	assert.Equal(t, "appdb", sidecarMetadata(t, nul)["listener"])
	require.NotNil(t, nul.GuardrailsInfo)
	var guardrails []models.SessionGuardRailsInfo
	require.NoError(t, json.Unmarshal([]byte(*nul.GuardrailsInfo), &guardrails))
	require.Len(t, guardrails, 1)
	assert.Equal(t, "nodrop", guardrails[0].RuleName)
	assert.Equal(t, "denyed", guardrails[0].Message)
	assert.Equal(t, "output", guardrails[0].Direction)
	_, _, entries := readStream(t, eventsColumnsOrgID, nulID)
	require.Len(t, entries, 2)
	assert.Equal(t, "DROP\x00 TABLE users", entries[0].Text, "the stream keeps the bytes: base64 carries a NUL")
	masked := readMaskedMetrics(t, nulID)
	require.Len(t, masked, 1)
	assert.Equal(t, maskedRow{InfoType: "email", CountMasked: 2}, masked[0])

	for sid, want := range map[string][2]string{
		"s-clickhouse": {"custom", "clickhouse"},
		"s-http":       {"httpproxy", "httpproxy"},
		"s-ssh":        {"application", "ssh"},
	} {
		row := getSidecarSessionRow(t, eventsColumnsOrgID, services.SidecarSessionID(sc.ID, sid))
		assert.Equal(t, want, [2]string{row.ConnectionType, row.ConnectionSubtype}, sid)
		require.NotNil(t, row.RecordingFormat, sid)
		assert.Equal(t, "raw", *row.RecordingFormat, "%s: never replayed as a terminal", sid)
	}

	// A connection name over the column is refused, not cut: two listeners
	// would share one name.
	rec := postEvents(sc, eventsBody(t, sessionEvent(1, "s-wide", 0, audit.KindSessionStart,
		func(e *audit.Event) { e.Connection = strings.Repeat("l", 128) })))
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Zero(t, countSessions(t, eventsColumnsOrgID, services.SidecarSessionID(sc.ID, "s-wide")))
}
