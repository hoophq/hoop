BEGIN;

SET search_path TO private;

ALTER TABLE connection_credentials ADD COLUMN IF NOT EXISTS session_id text;

COMMIT;
