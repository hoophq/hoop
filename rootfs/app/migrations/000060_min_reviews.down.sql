BEGIN;

SET search_path TO private;

ALTER TABLE connections DROP COLUMN IF EXISTS min_review_approvals;

COMMIT;
