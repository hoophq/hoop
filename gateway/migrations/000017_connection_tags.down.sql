BEGIN;

SET search_path TO private;

ALTER TABLE connections DROP COLUMN tags;

COMMIT;