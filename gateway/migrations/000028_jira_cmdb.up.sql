BEGIN;

SET search_path TO private;
ALTER TABLE jira_issue_templates ADD COLUMN cmdb_types JSONB NULL;

COMMIT;