BEGIN;

SET search_path TO private;

ALTER TABLE connection_credentials ADD COLUMN IF NOT EXISTS session_id text;
ALTER TABLE connection_credentials ADD CONSTRAINT IF NOT EXISTS uq_credentials_session_id UNIQUE (org_id, session_id);

COMMIT;
