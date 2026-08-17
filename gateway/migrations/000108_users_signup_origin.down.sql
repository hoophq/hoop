BEGIN;
SET search_path TO private;

ALTER TABLE users DROP COLUMN IF EXISTS signup_origin_other;
ALTER TABLE users DROP COLUMN IF EXISTS signup_origin;

COMMIT;
