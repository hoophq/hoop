BEGIN;
SET search_path TO private;

-- One row per append, so an append does not rewrite the whole stream.
-- A stream is its blob_stream followed by its chunks in id order.
CREATE TABLE IF NOT EXISTS session_stream_chunks (
    org_id UUID NOT NULL,
    blob_id UUID NOT NULL,
    id BIGINT GENERATED ALWAYS AS IDENTITY,
    entries JSONB NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    PRIMARY KEY (blob_id, id),
    FOREIGN KEY (org_id, blob_id) REFERENCES blobs (org_id, id) ON DELETE CASCADE
);

COMMIT;
