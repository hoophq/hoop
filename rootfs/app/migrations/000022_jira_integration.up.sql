BEGIN;

SET search_path TO private;

CREATE TYPE jira_integration_status AS ENUM ('enabled', 'disabled');

CREATE TABLE private.jira_integration (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    jira_url TEXT NOT NULL,
    jira_user TEXT NOT NULL,
    jira_api_token TEXT NOT NULL,
    jira_project_key TEXT NOT NULL,
    status jira_integration_status NOT NULL DEFAULT 'enabled',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

ALTER TABLE sessions ADD COLUMN jira_issue TEXT NULL;

COMMIT;
