BEGIN;

DROP TABLE private.jira_issue_templates;
ALTER TABLE private.connections DROP COLUMN jira_issue_template_id;

COMMIT;
