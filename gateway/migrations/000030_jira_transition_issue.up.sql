BEGIN;

SET search_path TO private;
ALTER TABLE jira_issue_templates ADD COLUMN issue_transition_name_on_close VARCHAR(50) NULL;

COMMIT;