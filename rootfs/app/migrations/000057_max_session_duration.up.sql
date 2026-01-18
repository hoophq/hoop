BEGIN;

SET search_path TO private;

ALTER TABLE connections ADD COLUMN IF NOT EXISTS access_max_duration INT;

COMMIT;