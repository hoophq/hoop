BEGIN;

SET search_path TO private;

DROP INDEX IF EXISTS index_reviews_sidecar_level_statement;

ALTER TABLE reviews DROP COLUMN IF EXISTS risk_level;

COMMIT;
