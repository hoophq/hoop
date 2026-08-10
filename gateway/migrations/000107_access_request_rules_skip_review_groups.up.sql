BEGIN;
SET search_path TO private;

-- User groups that bypass the approval review for this rule. Only honored
-- when approval_required_groups is empty (rule applies to everyone).
ALTER TABLE access_request_rules ADD COLUMN IF NOT EXISTS skip_review_groups TEXT[];

COMMIT;
