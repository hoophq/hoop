BEGIN;
SET search_path TO private;

DROP INDEX IF EXISTS uq_reviews_request_marker_pending;
DROP INDEX IF EXISTS idx_reviews_claim;

ALTER TABLE reviews
    DROP COLUMN IF EXISTS request_marker,
    DROP COLUMN IF EXISTS statement_hash;

COMMIT;
