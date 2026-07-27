BEGIN;

SET search_path TO private;
ALTER TABLE jira_issue_templates ADD COLUMN skip_transition_on_nonzero_exit_code BOOLEAN NOT NULL DEFAULT FALSE;

COMMIT;
