BEGIN;

SET search_path TO private;

CREATE TABLE jira_issue_templates(
    id uuid DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES orgs (id),
    jira_integration_id UUID NOT NULL REFERENCES jira_integrations (id) ON DELETE CASCADE,

    name VARCHAR(128) NOT NULL,
    description VARCHAR(255) NULL,
    project_key VARCHAR(10) NOT NULL,
    issue_type_name VARCHAR(255) NOT NULL,

    mapping_types JSONB NULL,
    prompt_types JSONB NULL,

    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),

    UNIQUE(org_id, name)
);

CREATE TABLE jira_issue_templates_connections(
    id uuid DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NULL REFERENCES orgs (id),

    jira_issue_template_id UUID NOT NULL REFERENCES jira_issue_templates (id) ON DELETE CASCADE,
    connection_id UUID NOT NULL REFERENCES connections (id) ON DELETE CASCADE,

    created_at TIMESTAMP DEFAULT NOW(),

    UNIQUE(jira_issue_template_id, connection_id)
);

ALTER TABLE private.jira_integrations DROP COLUMN project_key;

COMMIT;