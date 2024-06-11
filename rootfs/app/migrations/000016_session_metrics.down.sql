BEGIN;

SET search_path TO private;

ALTER TABLE private.sessions DROP COLUMN metrics;

COMMIT;