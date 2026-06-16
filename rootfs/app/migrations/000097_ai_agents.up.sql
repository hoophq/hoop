BEGIN;

SET search_path TO private;

CREATE TABLE IF NOT EXISTS ai_agents (
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

CREATE UNIQUE INDEX idx_ai_agents_org_name ON ai_agents(org_id, name);
CREATE UNIQUE INDEX idx_ai_agents_org_key_hash ON ai_agents(org_id, key_hash);
CREATE INDEX idx_ai_agents_org_status ON ai_agents(org_id, status);
CREATE INDEX idx_ai_agents_key_hash ON ai_agents(key_hash);

ALTER TABLE user_groups ADD COLUMN ai_agent_id UUID NULL REFERENCES ai_agents(id) ON DELETE CASCADE;
CREATE UNIQUE INDEX idx_user_groups_ai_agent_name ON user_groups(ai_agent_id, name) WHERE ai_agent_id IS NOT NULL;

COMMIT;