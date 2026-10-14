BEGIN;
SET search_path TO private;

-- The session list filters live sessions by status = 'open'. They are a few
-- rows in the largest table, so the index holds only them and serves the page
-- order and the count without reading closed sessions.
CREATE INDEX IF NOT EXISTS index_sessions_open
    ON sessions (org_id, created_at DESC, id DESC)
    WHERE status = 'open';

COMMIT;
