BEGIN;

SET search_path TO private;

DROP TABLE IF EXISTS session_interactions;
ALTER TABLE sessions DROP COLUMN IF EXISTS type;
DROP TYPE IF EXISTS enum_session_type;

COMMIT;