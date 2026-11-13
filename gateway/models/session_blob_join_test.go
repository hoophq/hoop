package models_test

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	otherOrgID      = "00000000-0000-0000-0000-0000000000b2"
	blobJoinSession = "00000000-0000-0000-0000-000000000c01"
	blobJoinInput   = "SELECT * FROM orders"
	blobJoinStream  = `[[0.1, "o", "YQ=="], [0.2, "e", "Yg=="]]`
)

func seedBlobJoinSession(t *testing.T) {
	t.Helper()
	require.NoError(t, models.UpsertSession(models.Session{
		ID:             blobJoinSession,
		OrgID:          testOrgID,
		Connection:     "pg-prod",
		ConnectionType: "database",
		Verb:           "exec",
		Status:         "open",
		BlobInput:      blobJoinInput,
		CreatedAt:      time.Now().UTC(),
	}))
	require.NoError(t, models.UpdateSessionEventStream(models.SessionDone{
		ID:         blobJoinSession,
		OrgID:      testOrgID,
		BlobStream: json.RawMessage(blobJoinStream),
		Status:     "done",
	}))
}

// The join the queries had before ENG-1139.
func idOnlyJoin(t *testing.T, query string) string {
	t.Helper()
	legacy := strings.Replace(query, "b.org_id = s.org_id AND ", "", 1)
	require.NotEqual(t, query, legacy)
	return legacy
}

func TestSessionBlobReads(t *testing.T) {
	startTestDB(t)
	seedBlobJoinSession(t)

	sess, err := models.GetSessionByID(testOrgID, blobJoinSession)
	require.NoError(t, err)
	assert.True(t, sess.BlobInputID.Valid)
	assert.EqualValues(t, len(blobJoinInput), sess.BlobInputSize)

	input, err := sess.GetBlobInput()
	require.NoError(t, err)
	assert.Equal(t, models.BlobInputType(blobJoinInput), input)

	stream, err := sess.GetBlobStream()
	require.NoError(t, err)
	require.NotNil(t, stream)
	assert.Equal(t, testOrgID, stream.OrgID)
	assert.Equal(t, "session-stream", stream.Type)
	assert.JSONEq(t, blobJoinStream, string(stream.BlobStream))

	var legacy models.Session
	require.NoError(t, models.DB.Raw(idOnlyJoin(t, models.SessionByIDQuery), testOrgID, blobJoinSession).
		First(&legacy).Error)
	assert.Equal(t, legacy, *sess)

	for _, query := range []string{models.SessionBlobInputQuery, models.SessionBlobStreamQuery} {
		var got, want models.Blob
		require.NoError(t, models.DB.Raw(query, testOrgID, blobJoinSession).First(&got).Error)
		require.NoError(t, models.DB.Raw(idOnlyJoin(t, query), testOrgID, blobJoinSession).First(&want).Error)
		assert.Equal(t, want, got)
	}
}

func TestSessionBlobReadsWithChunks(t *testing.T) {
	startTestDB(t)
	require.NoError(t, models.UpsertSession(models.Session{
		ID:             blobJoinSession,
		OrgID:          testOrgID,
		Connection:     "pg-prod",
		ConnectionType: "database",
		Verb:           "connect",
		Status:         "open",
		CreatedAt:      time.Now().UTC(),
	}))
	require.NoError(t, models.CreateEmptySessionStreamBlob(testOrgID, blobJoinSession, nil))
	require.NoError(t, models.AppendSessionStreamChunkTx(models.DB, testOrgID, blobJoinSession,
		json.RawMessage(blobJoinStream)))

	sess, err := models.GetSessionByID(testOrgID, blobJoinSession)
	require.NoError(t, err)
	stream, err := sess.GetBlobStream()
	require.NoError(t, err)
	require.NotNil(t, stream)
	assert.JSONEq(t, blobJoinStream, string(stream.BlobStream))
}

// A session reads only blobs of its own org. The id-only join read this one.
func TestSessionBlobReadsIgnoreOtherOrgBlob(t *testing.T) {
	startTestDB(t)
	seedBlobJoinSession(t)
	execSQL(t, `INSERT INTO private.orgs (id, name) VALUES (?, 'blob-join-other')`, otherOrgID)
	execSQL(t, `UPDATE private.blobs SET org_id = ? WHERE org_id = ?`, otherOrgID, testOrgID)

	sess, err := models.GetSessionByID(testOrgID, blobJoinSession)
	require.NoError(t, err)
	assert.Zero(t, sess.BlobInputSize)

	input, err := sess.GetBlobInput()
	require.NoError(t, err)
	assert.Empty(t, input)

	stream, err := sess.GetBlobStream()
	require.NoError(t, err)
	assert.Nil(t, stream)
}

// The index lookup gives both columns of blobs_org_id_id_key.
var blobIndexLookup = regexp.MustCompile(
	`blobs_org_id_id_key[^\n]*\n\s*Index Cond: \(\(org_id = [^\n]*AND \(id = s\.blob_(input|stream)_id\)`)

func TestSessionBlobJoinsUseOrgIndex(t *testing.T) {
	startTestDB(t)
	seedBlobJoinSession(t)
	// Enough rows that the planner does not scan the table whatever the join.
	execSQL(t, `INSERT INTO private.blobs (org_id, type, blob_stream)
		SELECT ?, 'session-input', '["x"]'::jsonb FROM generate_series(1, 5000)`, testOrgID)
	execSQL(t, `ANALYZE private.blobs`)
	execSQL(t, `ANALYZE private.sessions`)

	explain := func(query string) string {
		t.Helper()
		rows, err := models.DB.Raw(`EXPLAIN `+query, testOrgID, blobJoinSession).Rows()
		require.NoError(t, err)
		defer rows.Close()
		var plan []string
		for rows.Next() {
			var line string
			require.NoError(t, rows.Scan(&line))
			plan = append(plan, line)
		}
		require.NoError(t, rows.Err())
		return strings.Join(plan, "\n")
	}

	for name, query := range map[string]string{
		"GetSessionByID": models.SessionByIDQuery,
		"GetBlobInput":   models.SessionBlobInputQuery,
		"GetBlobStream":  models.SessionBlobStreamQuery,
	} {
		t.Run(name, func(t *testing.T) {
			plan := explain(query)
			assert.Regexp(t, blobIndexLookup, plan)
			assert.NotContains(t, plan, "Seq Scan on blobs")

			assert.NotRegexp(t, blobIndexLookup, explain(idOnlyJoin(t, query)),
				"the id-only join must fail this test")
		})
	}
}
