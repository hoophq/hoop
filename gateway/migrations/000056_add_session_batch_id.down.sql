BEGIN;

SET search_path TO private;

-- Remove index
DROP INDEX IF EXISTS index_sessions_batch_id;

-- Remove column
ALTER TABLE sessions DROP COLUMN IF EXISTS session_batch_id;

COMMIT;

