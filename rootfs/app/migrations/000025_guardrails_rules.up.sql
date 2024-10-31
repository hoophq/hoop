BEGIN;

SET search_path TO private;

CREATE TABLE guardrail_rules(
    id uuid DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES orgs (id),

    name VARCHAR(128) NOT NULL,
    input JSONB NULL,
    output JSONB NULL,

    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),

    UNIQUE(org_id, name)
);

CREATE TABLE guardrail_rules_connections(
    id uuid DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NULL REFERENCES orgs (id),

    rule_id UUID NOT NULL REFERENCES guardrail_rules (id) ON DELETE CASCADE,
    connection_id UUID NOT NULL REFERENCES connections (id) ON DELETE CASCADE,

    created_at TIMESTAMP DEFAULT NOW(),

    UNIQUE(rule_id, connection_id)
);

COMMIT;