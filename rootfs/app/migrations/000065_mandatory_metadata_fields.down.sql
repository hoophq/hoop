BEGIN;

SET search_path TO private;

ALTER TABLE private.connections DROP COLUMN IF EXISTS mandatory_metadata_fields;

COMMIT;
