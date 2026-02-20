BEGIN;

SET search_path TO private;

DROP TABLE access_control_rules;

ALTER TABLE reviews DROP COLUMN access_request_rule_name;
ALTER TABLE reviews DROP COLUMN min_approvals;
ALTER TABLE reviews DROP COLUMN force_approval_groups;

COMMIT;
