BEGIN;

-- Drop the table
DROP TABLE IF EXISTS jira_integrations;

-- Drop the enum type
DROP TYPE IF EXISTS jira_integration_status;

ALTER TABLE sessions DROP COLUMN jira_issue;

COMMIT;
