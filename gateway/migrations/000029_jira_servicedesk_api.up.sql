BEGIN;

SET search_path TO private;
ALTER TABLE jira_issue_templates ADD COLUMN request_type_id VARCHAR(20) NULL;
ALTER TABLE jira_issue_templates DROP COLUMN issue_type_name;

COMMIT;