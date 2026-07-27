BEGIN;

SET search_path TO private;

ALTER TABLE connections ADD COLUMN IF NOT EXISTS force_approve_groups TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE review_groups ADD COLUMN IF NOT EXISTS forced_review BOOLEAN NOT NULL DEFAULT FALSE;

COMMIT;
