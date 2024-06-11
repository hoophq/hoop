BEGIN;

SET search_path TO private;

ALTER TABLE private.sessions ADD COLUMN metrics JSONB NULL;

COMMIT;
