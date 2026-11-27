BEGIN;
SET search_path TO private;

DROP INDEX IF EXISTS idx_sidecars_id_text;

COMMIT;
