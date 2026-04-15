BEGIN;

SET search_path TO private;

CREATE TABLE IF NOT EXISTS api_keys (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES orgs(id),
    name VARCHAR(255) NOT NULL,
    key_hash VARCHAR(64) NOT NULL,
    masked_key VARCHAR(255) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    created_by VARCHAR(255) NOT NULL,
    deactivated_by VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deactivated_at TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX idx_api_keys_org_name ON api_keys(org_id, name);
CREATE UNIQUE INDEX idx_api_keys_org_key_hash ON api_keys(org_id, key_hash);
CREATE INDEX idx_api_keys_org_status ON api_keys(org_id, status);

ALTER TABLE user_groups ADD COLUMN api_key_id UUID NULL REFERENCES api_keys(id) ON DELETE CASCADE;
CREATE UNIQUE INDEX idx_user_groups_api_key_name ON user_groups(api_key_id, name) WHERE api_key_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS api_keys_connections (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES orgs(id),
    api_key_id UUID NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    connection_id UUID NOT NULL REFERENCES connections(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(api_key_id, connection_id)
);

COMMIT;