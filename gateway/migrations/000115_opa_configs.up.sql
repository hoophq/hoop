BEGIN;

SET search_path TO private;

CREATE TABLE IF NOT EXISTS opa_configs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES orgs(id),
    name VARCHAR(255) NOT NULL,
    url TEXT NOT NULL,
    timeout_sec INT NOT NULL DEFAULT 0,
    fail_open BOOLEAN NOT NULL DEFAULT FALSE,
    gate BOOLEAN NOT NULL DEFAULT FALSE,
    created_by VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_opa_configs_org_name ON opa_configs(org_id, name);

ALTER TABLE connections ADD COLUMN opa_config_id UUID NULL
    REFERENCES opa_configs(id) ON DELETE RESTRICT;
CREATE INDEX idx_connections_opa_config_id ON connections(opa_config_id)
    WHERE opa_config_id IS NOT NULL;

COMMIT;
