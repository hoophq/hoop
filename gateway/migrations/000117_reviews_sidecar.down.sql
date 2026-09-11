BEGIN;

SET search_path TO private;

DROP INDEX IF EXISTS index_reviews_sidecar_id;
ALTER TABLE reviews DROP COLUMN IF EXISTS listener_name;
ALTER TABLE reviews DROP COLUMN IF EXISTS sidecar_id;

COMMIT;
