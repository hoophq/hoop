BEGIN;

SET search_path TO private;

CREATE TABLE agent_identities (
    id          TEXT        NOT NULL PRIMARY KEY,
    org_id      TEXT        NOT NULL REFERENCES orgs(id),
    subject     TEXT        NOT NULL UNIQUE,
    name        TEXT        NOT NULL,
    status      TEXT        NOT NULL DEFAULT 'active',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_agent_identities_org_id ON agent_identities(org_id);

CREATE TABLE agent_identity_secrets (
    id                  TEXT        NOT NULL PRIMARY KEY,
    agent_identity_id   TEXT        NOT NULL REFERENCES agent_identities(id) ON DELETE CASCADE,
    key_prefix          TEXT        NOT NULL,
    hashed_secret       TEXT        NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at          TIMESTAMPTZ
);
CREATE INDEX idx_agent_identity_secrets_agent_id ON agent_identity_secrets(agent_identity_id);

COMMIT;
