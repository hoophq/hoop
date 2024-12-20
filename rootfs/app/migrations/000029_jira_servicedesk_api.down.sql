BEGIN;

SET search_path TO private;
ALTER TABLE jira_issue_templates DROP COLUMN request_type_id;
ALTER TABLE jira_issue_templates ADD COLUMN issue_type_name VARCHAR(255) NULL;

COMMIT;