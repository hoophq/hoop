BEGIN;

SET search_path TO private;

ALTER TABLE connections DROP COLUMN IF EXISTS force_approve_groups;
ALTER TABLE review_groups DROP COLUMN IF EXISTS forced_review;

COMMIT;
