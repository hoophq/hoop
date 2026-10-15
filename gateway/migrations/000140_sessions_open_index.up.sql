-- The session list filters live sessions by status = 'open'. They are a few
-- rows in the largest table, so the index holds only them and serves the page
-- order and the count without reading closed sessions.
--
-- A plain, blocking CREATE INDEX on purpose, as in 000109 and 000113: a failed
-- CONCURRENTLY build leaves an INVALID index that IF NOT EXISTS then skips, and
-- a dirty schema_migrations row that stops gateway startup. The build holds a
-- SHARE lock on sessions while it scans the table; it took 137 ms on 500,000
-- sessions.
BEGIN;
SET search_path TO private;

CREATE INDEX IF NOT EXISTS index_sessions_open
    ON sessions (org_id, created_at DESC, id DESC)
    WHERE status = 'open';

COMMIT;
