BEGIN;

SET search_path TO private;

DROP INDEX IF EXISTS index_sessions_correlation_id;

ALTER TABLE sessions
DROP COLUMN IF EXISTS correlation_id;

COMMIT;