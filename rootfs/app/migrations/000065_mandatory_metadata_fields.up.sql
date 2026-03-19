BEGIN;

SET search_path TO private;

ALTER TABLE connections ADD COLUMN IF NOT EXISTS mandatory_metadata_fields text[];

COMMIT;
