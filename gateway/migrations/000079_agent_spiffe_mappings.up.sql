BEGIN;

SET search_path TO private;

CREATE TABLE agent_spiffe_mappings (
    id             UUID DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id         UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,

    trust_domain   TEXT NOT NULL,
    spiffe_id      TEXT,
    spiffe_prefix  TEXT,

    agent_id       UUID REFERENCES agents(id) ON DELETE CASCADE,
    agent_template TEXT,

    groups         TEXT[] NOT NULL DEFAULT '{}',

    created_at     TIMESTAMP DEFAULT NOW(),
    updated_at     TIMESTAMP DEFAULT NOW(),

    CONSTRAINT spiffe_match_xor CHECK ((spiffe_id IS NOT NULL) <> (spiffe_prefix IS NOT NULL)),
    CONSTRAINT spiffe_resolve_xor CHECK ((agent_id IS NOT NULL) <> (agent_template IS NOT NULL))
);

CREATE UNIQUE INDEX idx_agent_spiffe_mappings_exact
    ON agent_spiffe_mappings(org_id, spiffe_id)
    WHERE spiffe_id IS NOT NULL;

CREATE INDEX idx_agent_spiffe_mappings_prefix
    ON agent_spiffe_mappings(org_id, spiffe_prefix)
    WHERE spiffe_prefix IS NOT NULL;

CREATE INDEX idx_agent_spiffe_mappings_trust_domain
    ON agent_spiffe_mappings(org_id, trust_domain);

COMMIT;
