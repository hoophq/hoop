BEGIN;

SET search_path TO private;

-- Create resources table
CREATE TABLE resources (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    name VARCHAR(128) NOT NULL,
    type VARCHAR(64) NOT NULL,
    agent_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, name)
);

-- Migrate existing connection subtypes to resources
INSERT INTO resources (org_id, name, type, agent_id)
    SELECT org_id, name, COALESCE(subtype, ''), agent_id
    FROM connections;

-- Add resource_name column to connections table
ALTER TABLE connections
    ADD COLUMN resource_name VARCHAR(128);

-- Update connections to reference the new resources
UPDATE connections
    SET resource_name = name;

-- Add foreign key constraint and set resource_name as NOT NULL
ALTER TABLE connections
    ALTER COLUMN resource_name SET NOT NULL,
    ADD CONSTRAINT connection_resource_fk
        FOREIGN KEY (org_id, resource_name)
        REFERENCES resources(org_id, name)
        ON UPDATE CASCADE;

COMMIT;