BEGIN;

SET search_path TO private;

ALTER TABLE connections DROP COLUMN tags;
ALTER TABLE connections ADD COLUMN tags TEXT[] NULL;

COMMIT;