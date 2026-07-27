BEGIN;
SET search_path TO private;

ALTER TABLE connections DROP COLUMN IF EXISTS secrets_updated_at;

COMMIT;
