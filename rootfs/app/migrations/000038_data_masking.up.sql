BEGIN;

SET search_path TO private;

CREATE TABLE datamasking_rules(
    id uuid DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES orgs (id),

    name VARCHAR(128) NOT NULL,
    description VARCHAR(512) NULL,
    supported_entity_types JSONB NULL,
    custom_entity_types JSONB NULL,

    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),

    UNIQUE(org_id, name)
);

CREATE TYPE enum_datamasking_assoc_status AS ENUM ('active', 'inactive');

CREATE TABLE datamasking_rules_connections(
    id uuid DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NULL REFERENCES orgs (id),

    rule_id UUID NOT NULL REFERENCES datamasking_rules (id) ON DELETE CASCADE,
    connection_id UUID NOT NULL REFERENCES connections (id) ON DELETE CASCADE,
    status enum_datamasking_assoc_status DEFAULT 'active',

    created_at TIMESTAMP DEFAULT NOW(),

    UNIQUE(rule_id, connection_id)
);

COMMIT;