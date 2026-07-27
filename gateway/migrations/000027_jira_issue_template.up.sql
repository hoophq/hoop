BEGIN;

SET search_path TO private;

CREATE TABLE jira_issue_templates(
    id uuid DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES orgs (id),
    jira_integration_id UUID NOT NULL REFERENCES jira_integrations (id),

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

ALTER TABLE private.connections
ADD COLUMN jira_issue_template_id UUID NULL
CONSTRAINT jira_issue_templates_fk REFERENCES jira_issue_templates (id)
ON DELETE SET NULL;

ALTER TABLE private.sessions ADD COLUMN integrations_metadata JSONB NULL;
ALTER TABLE private.jira_integrations DROP COLUMN project_key;

COMMIT;