BEGIN;

SET search_path TO private;

DROP INDEX IF EXISTS index_reviews_sidecar_statement;
ALTER TABLE reviews DROP COLUMN IF EXISTS statement_hash;

COMMIT;
