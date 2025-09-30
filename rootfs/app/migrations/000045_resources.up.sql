BEGIN;

SET search_path TO private;

CREATE TABLE resources (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    name VARCHAR(128) NOT NULL,
    type VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, name)
);

INSERT INTO resources (org_id, name, type)
    SELECT org_id, name, subtype
    FROM connections;

ALTER TABLE connections
    ADD COLUMN resource_name VARCHAR(128),
    ADD CONSTRAINT fk_connection_resource
        FOREIGN KEY (org_id, resource_name)
        REFERENCES resources(org_id, name)
        ON DELETE CASCADE;

COMMIT;