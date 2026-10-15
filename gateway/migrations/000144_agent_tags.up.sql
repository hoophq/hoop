BEGIN;

SET search_path TO private;

-- Tags of an agent. They live apart from connection_tags: agents and
-- connections keep separate tag data.
CREATE TABLE agent_tags(
    org_id UUID NOT NULL REFERENCES orgs (id),
    agent_id UUID NOT NULL REFERENCES agents (id) ON DELETE CASCADE,

    key VARCHAR(64) NOT NULL,
    value VARCHAR(256) NOT NULL,

    created_at TIMESTAMP NOT NULL DEFAULT NOW(),

    PRIMARY KEY (agent_id, key)
);

CREATE INDEX agent_tags_org_id_key_value_idx ON agent_tags (org_id, key, value);

COMMIT;
