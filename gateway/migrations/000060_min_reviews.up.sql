BEGIN;

SET search_path TO private;

ALTER TABLE connections ADD COLUMN IF NOT EXISTS min_review_approvals INT;

COMMIT;
