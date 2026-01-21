BEGIN;

SET search_path TO private;

ALTER TABLE connections DROP COLUMN IF EXISTS access_max_duration;

COMMIT;