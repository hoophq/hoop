BEGIN;
SET search_path TO private;

DROP INDEX IF EXISTS index_review_groups_review_id;
DROP INDEX IF EXISTS index_reviews_org_status;

COMMIT;
