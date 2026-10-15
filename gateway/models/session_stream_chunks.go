package models

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"

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

// withStreamChunks appends the chunks of blob to its blob_stream. It joins one
// chunk at a time into one buffer: holding every chunk next to the joined copy
// doubles the memory of reading a large session.
func withStreamChunks(tx *gorm.DB, blob *Blob) error {
	var count int
	if err := tx.Raw(`SELECT count(*) FROM private.session_stream_chunks
		WHERE org_id = ? AND blob_id = ?`, blob.OrgID, blob.ID).Scan(&count).Error; err != nil {
		return fmt.Errorf("failed reading the session stream chunks: %w", err)
	}
	if count == 0 {
		return nil
	}
	rows, err := tx.Raw(`SELECT entries FROM private.session_stream_chunks
		WHERE org_id = ? AND blob_id = ? ORDER BY id`, blob.OrgID, blob.ID).Rows()
	if err != nil {
		return fmt.Errorf("failed reading the session stream chunks: %w", err)
	}
	defer rows.Close()

	var j jsonArrayJoiner
	if err := j.add(blob.BlobStream); err != nil {
		return fmt.Errorf("failed joining the session stream chunks of blob %s: %w", blob.ID, err)
	}
	for first := true; rows.Next(); first = false {
		var entries sql.RawBytes
		if err := rows.Scan(&entries); err != nil {
			return fmt.Errorf("failed reading the session stream chunks: %w", err)
		}
		if first {
			// chunks are flush windows of about the same size
			j.grow(len(entries) * count)
		}
		if err := j.add(entries); err != nil {
			return fmt.Errorf("failed joining the session stream chunks of blob %s: %w", blob.ID, err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed reading the session stream chunks: %w", err)
	}
	blob.BlobStream = j.bytes()
	return nil
}

// concatJSONArrays joins JSON arrays into one without decoding their elements.
func concatJSONArrays(parts []json.RawMessage) (json.RawMessage, error) {
	var j jsonArrayJoiner
	size := 2
	for _, p := range parts {
		size += len(p) + 1
	}
	j.grow(size)
	for _, p := range parts {
		if err := j.add(p); err != nil {
			return nil, err
		}
	}
	return j.bytes(), nil
}

// jsonArrayJoiner joins JSON arrays into one, one array at a time, without
// decoding their elements.
type jsonArrayJoiner struct {
	out   []byte // "[" and the elements so far, without the closing "]"
	parts int
}

func (j *jsonArrayJoiner) grow(n int) { j.out = slices.Grow(j.out, n) }

func (j *jsonArrayJoiner) add(p []byte) error {
	p = bytes.TrimSpace(p)
	if len(p) < 2 || p[0] != '[' || p[len(p)-1] != ']' {
		return fmt.Errorf("part %d is not a JSON array", j.parts)
	}
	j.parts++
	inner := bytes.TrimSpace(p[1 : len(p)-1])
	if len(inner) == 0 {
		return nil
	}
	if len(j.out) == 0 {
		j.out = append(j.out, '[')
	} else {
		j.out = append(j.out, ',')
	}
	j.out = append(j.out, inner...)
	return nil
}

func (j *jsonArrayJoiner) bytes() json.RawMessage {
	if len(j.out) == 0 {
		return json.RawMessage(`[]`)
	}
	return append(j.out, ']')
}
