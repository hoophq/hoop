BEGIN;

SET search_path TO private;

ALTER TABLE connection_credentials DROP CONSTRAINT IF EXISTS uq_credentials_session_id;
ALTER TABLE connection_credentials DROP COLUMN IF EXISTS session_id;

COMMIT;
