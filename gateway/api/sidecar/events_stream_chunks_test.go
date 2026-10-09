package apisidecar

import (
	"encoding/json"
	"testing"

	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	eventsChunksOrgID      = "00000000-0000-0000-0000-0000000000f1"
	eventsChunksFlagOrgID  = "00000000-0000-0000-0000-0000000000f2"
	eventsChunksReplaceOrg = "00000000-0000-0000-0000-0000000000f3"
)

// fullSessionStream is the stream of fullSession, whatever the batches.
var fullSessionStream = []streamEntry{
	{1, "i", "SELECT * FROM users"},
	{2, "i", "DROP TABLE users"},
}

// storedStream is how a session's stream is stored: the blob and its chunks.
func storedStream(t *testing.T, orgID, sessionID string) (blob string, chunks int64) {
	t.Helper()
	require.NoError(t, models.DB.Raw(`SELECT blob_stream::text FROM private.blobs WHERE org_id = ? AND id = ?`,
		orgID, models.SessionStreamBlobID(sessionID)).Scan(&blob).Error)
	require.NoError(t, models.DB.Raw(`SELECT count(*) FROM private.session_stream_chunks WHERE org_id = ? AND blob_id = ?`,
		orgID, models.SessionStreamBlobID(sessionID)).Scan(&chunks).Error)
	return blob, chunks
}

// assertFullSessionStream checks the stream fullSession leaves, in order.
func assertFullSessionStream(t *testing.T, orgID, sessionID string) {
	t.Helper()
	raw, _, entries := readStream(t, orgID, sessionID)
	require.Len(t, entries, 4, raw)
	assert.Equal(t, fullSessionStream, entries[:2])
	assert.Equal(t, "e", entries[2].Kind)
	assert.Contains(t, entries[2].Text, "no-drop")
	assert.Equal(t, streamEntry{4, "e", "upstream closed the connection"}, entries[3])
}

// Each batch is one chunk; the blob stays empty and the reader joins them.
func TestPostEventsStreamChunks(t *testing.T) {
	startEventsDB(t, eventsChunksOrgID)
	enableSessionEvents(t, eventsChunksOrgID)
	sc := seedEventsSidecar(t, eventsChunksOrgID, "edge-chunks")
	events := fullSession("s-chunks")
	for _, batch := range [][2]int{{0, 2}, {2, 4}, {4, 6}} {
		decodeEventsResponse(t, postEvents(sc, eventsBody(t, events[batch[0]:batch[1]]...)))
	}
	id := services.SidecarSessionID(sc.ID, "s-chunks")

	blob, chunks := storedStream(t, eventsChunksOrgID, id)
	assert.Equal(t, "[]", blob)
	assert.EqualValues(t, 3, chunks)
	assertFullSessionStream(t, eventsChunksOrgID, id)

	err := models.AppendSessionStreamChunkTx(models.DB, eventsChunksOrgID, "no-such-session", json.RawMessage(`[]`))
	assert.ErrorIs(t, err, models.ErrNotFound, "a missing blob is retried, not refused")
}

// The flag changes the layout mid-session; the entries stay in order.
func TestPostEventsStreamChunksFlag(t *testing.T) {
	startEventsDB(t, eventsChunksFlagOrgID)
	enableSessionEvents(t, eventsChunksFlagOrgID)
	sc := seedEventsSidecar(t, eventsChunksFlagOrgID, "edge-chunks-flag")

	t.Run("off then on: the blob, then chunks", func(t *testing.T) {
		events := fullSession("s-off-on")
		id := services.SidecarSessionID(sc.ID, "s-off-on")
		featureflag.Set(eventsChunksFlagOrgID, services.SidecarStreamChunksFlag, false)
		decodeEventsResponse(t, postEvents(sc, eventsBody(t, events[:3]...)))
		blob, chunks := storedStream(t, eventsChunksFlagOrgID, id)
		assert.NotEqual(t, "[]", blob)
		assert.Zero(t, chunks)

		featureflag.Set(eventsChunksFlagOrgID, services.SidecarStreamChunksFlag, true)
		decodeEventsResponse(t, postEvents(sc, eventsBody(t, events[3:]...)))
		_, chunks = storedStream(t, eventsChunksFlagOrgID, id)
		assert.EqualValues(t, 1, chunks)
		assertFullSessionStream(t, eventsChunksFlagOrgID, id)
	})

	t.Run("on then off: chunks only", func(t *testing.T) {
		events := fullSession("s-on-off")
		id := services.SidecarSessionID(sc.ID, "s-on-off")
		featureflag.Set(eventsChunksFlagOrgID, services.SidecarStreamChunksFlag, true)
		decodeEventsResponse(t, postEvents(sc, eventsBody(t, events[:3]...)))

		featureflag.Set(eventsChunksFlagOrgID, services.SidecarStreamChunksFlag, false)
		decodeEventsResponse(t, postEvents(sc, eventsBody(t, events[3:]...)))
		blob, chunks := storedStream(t, eventsChunksFlagOrgID, id)
		assert.Equal(t, "[]", blob, "a blob append would land before the chunks")
		assert.EqualValues(t, 2, chunks)
		assertFullSessionStream(t, eventsChunksFlagOrgID, id)
	})
}

// UpdateSessionEventStream replaces the whole stream, chunks included.
func TestUpdateSessionEventStreamDropsChunks(t *testing.T) {
	startEventsDB(t, eventsChunksReplaceOrg)
	enableSessionEvents(t, eventsChunksReplaceOrg)
	sc := seedEventsSidecar(t, eventsChunksReplaceOrg, "edge-chunks-replace")
	decodeEventsResponse(t, postEvents(sc, eventsBody(t, fullSession("s-replace")...)))
	id := services.SidecarSessionID(sc.ID, "s-replace")

	require.NoError(t, models.UpdateSessionEventStream(models.SessionDone{
		ID:         id,
		OrgID:      eventsChunksReplaceOrg,
		BlobStream: json.RawMessage(`[[0, "e", "cmVwbGFjZWQ="]]`),
		Status:     "done",
		Metrics:    map[string]any{},
	}))
	_, chunks := storedStream(t, eventsChunksReplaceOrg, id)
	assert.Zero(t, chunks)
	_, _, entries := readStream(t, eventsChunksReplaceOrg, id)
	assert.Equal(t, []streamEntry{{0, "e", "replaced"}}, entries)
}
