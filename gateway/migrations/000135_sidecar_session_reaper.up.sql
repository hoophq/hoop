BEGIN;
SET search_path TO private;

-- The sidecar session reaper scans open sidecar sessions every minute. They
-- are a few rows in the largest table, so the index holds only them.
CREATE INDEX IF NOT EXISTS index_sessions_open_sidecar
    ON sessions (created_at)
    WHERE origin = 'sidecar' AND status = 'open';

COMMIT;
