BEGIN;

SET search_path TO private;

ALTER TABLE connections ADD COLUMN tags JSONB NULL;

COMMIT;