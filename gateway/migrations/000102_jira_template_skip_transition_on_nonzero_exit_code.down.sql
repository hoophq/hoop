BEGIN;

SET search_path TO private;
ALTER TABLE jira_issue_templates DROP COLUMN skip_transition_on_nonzero_exit_code;

COMMIT;
