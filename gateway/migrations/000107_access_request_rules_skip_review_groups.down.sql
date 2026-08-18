BEGIN;
SET search_path TO private;
ALTER TABLE access_request_rules DROP COLUMN IF EXISTS skip_review_groups;
COMMIT;
