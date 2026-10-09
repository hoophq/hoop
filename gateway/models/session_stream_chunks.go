package models

import (
	"bytes"
	"encoding/json"
	"fmt"

	"gorm.io/gorm"
)

// AppendSessionStreamChunkTx appends entries to the session's stream as a new
// chunk row. Unlike AppendSessionStreamTx it does not rewrite the stream, so
// its cost does not grow with the session. entries must be a JSON array.
// Returns ErrNotFound if the stream blob row does not exist.
//
// Chunks are ordered by id: callers must serialize appends to one session.
func AppendSessionStreamChunkTx(tx *gorm.DB, orgID, sessionID string, entries json.RawMessage) error {
	// INSERT ... SELECT, not a plain INSERT: a missing blob must be ErrNotFound,
	// which callers retry, not a foreign key violation, which they refuse.
	res := tx.Exec(`
	INSERT INTO private.session_stream_chunks (org_id, blob_id, entries)
	SELECT org_id, id, ?::jsonb FROM private.blobs WHERE org_id = ? AND id = ?`,
		string(entries), orgID, SessionStreamBlobID(sessionID))
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// HasSessionStreamChunksTx reports whether the session's stream has chunks.
// Once it has, every later append must be a chunk to keep the order.
func HasSessionStreamChunksTx(tx *gorm.DB, orgID, sessionID string) (bool, error) {
	var found bool
	err := tx.Raw(`SELECT EXISTS (
		SELECT 1 FROM private.session_stream_chunks WHERE org_id = ? AND blob_id = ?)`,
		orgID, SessionStreamBlobID(sessionID)).Scan(&found).Error
	return found, err
}

// withStreamChunks appends the chunks of blob to its blob_stream.
func withStreamChunks(tx *gorm.DB, blob *Blob) error {
	var rows []struct {
		Entries json.RawMessage `gorm:"column:entries"`
	}
	err := tx.Raw(`SELECT entries FROM private.session_stream_chunks
		WHERE org_id = ? AND blob_id = ? ORDER BY id`, blob.OrgID, blob.ID).
		Scan(&rows).Error
	if err != nil {
		return fmt.Errorf("failed reading the session stream chunks: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}
	parts := make([]json.RawMessage, 0, len(rows)+1)
	parts = append(parts, blob.BlobStream)
	for _, r := range rows {
		parts = append(parts, r.Entries)
	}
	stream, err := concatJSONArrays(parts)
	if err != nil {
		return fmt.Errorf("failed joining the session stream chunks of blob %s: %w", blob.ID, err)
	}
	blob.BlobStream = stream
	return nil
}

// concatJSONArrays joins JSON arrays into one without decoding their elements.
func concatJSONArrays(parts []json.RawMessage) (json.RawMessage, error) {
	size := 2
	for _, p := range parts {
		size += len(p) + 1
	}
	out := make([]byte, 0, size)
	out = append(out, '[')
	empty := true
	for i, p := range parts {
		p = bytes.TrimSpace(p)
		if len(p) < 2 || p[0] != '[' || p[len(p)-1] != ']' {
			return nil, fmt.Errorf("part %d is not a JSON array", i)
		}
		inner := bytes.TrimSpace(p[1 : len(p)-1])
		if len(inner) == 0 {
			continue
		}
		if !empty {
			out = append(out, ',')
		}
		out = append(out, inner...)
		empty = false
	}
	return append(out, ']'), nil
}
