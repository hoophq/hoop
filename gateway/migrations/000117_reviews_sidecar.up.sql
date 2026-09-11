BEGIN;

SET search_path TO private;

-- A review filed by a sidecar binds to one of its listeners. There is no
-- connection: 000116 dropped connections.sidecar_id, and listeners live inside
-- sidecars.configuration. The connection_* columns stay empty for these rows.
ALTER TABLE reviews ADD COLUMN IF NOT EXISTS sidecar_id UUID NULL
    REFERENCES sidecars(id) ON DELETE SET NULL;
ALTER TABLE reviews ADD COLUMN IF NOT EXISTS listener_name VARCHAR(255) NULL;

-- Partial: only sidecar reviews carry the column, and they are the minority.
CREATE INDEX IF NOT EXISTS index_reviews_sidecar_id ON reviews (sidecar_id)
    WHERE sidecar_id IS NOT NULL;

COMMIT;
