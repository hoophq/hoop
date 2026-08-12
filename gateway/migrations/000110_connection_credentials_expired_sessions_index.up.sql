BEGIN;
SET search_path TO private;

-- Supports the credentialsweeper job's tick query, which takes the oldest
-- credentials whose access window has elapsed but whose audit session is still
-- linked:
--
--   WHERE expire_at < $1 AND session_id IS NOT NULL AND session_id != ''
--   ORDER BY expire_at LIMIT $2 FOR UPDATE SKIP LOCKED
--
-- The predicate matches that WHERE clause exactly so the planner can use the
-- partial index, and the expire_at key order serves the ORDER BY ... LIMIT
-- without a sort. Without it every tick is a seq scan of the table.
--
-- Only rows still holding a session_id are indexed, which is the small
-- minority: the sweeper (and the explicit close/issue paths) NULL the column
-- as soon as the session is finalised, so swept and persistent credentials
-- drop straight back out of the index.
CREATE INDEX IF NOT EXISTS idx_conn_cred_expired_sessions
    ON connection_credentials (expire_at)
    WHERE session_id IS NOT NULL AND session_id != '';

COMMIT;
