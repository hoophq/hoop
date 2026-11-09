BEGIN;

SET search_path TO private;

ALTER TABLE reviews DROP COLUMN IF EXISTS executed_by;

COMMIT;
