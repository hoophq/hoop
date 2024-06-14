BEGIN;

SET search_path TO private;

ALTER TABLE connections ADD COLUMN _tags TEXT[] NULL;

COMMIT;