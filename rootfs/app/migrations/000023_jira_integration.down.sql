BEGIN;

-- Drop the table
DROP TABLE IF EXISTS private.jira_integration;

-- Drop the enum type
DROP TYPE IF EXISTS private.jira_integration_status;

ALTER TABLE sessions DROP COLUMN jira_issue;

COMMIT;
