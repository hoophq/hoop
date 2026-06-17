BEGIN;

SET search_path TO private;

ALTER TABLE sessions
ADD COLUMN origin VARCHAR(64) NULL;

COMMIT;
