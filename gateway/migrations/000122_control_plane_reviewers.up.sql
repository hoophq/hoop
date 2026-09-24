BEGIN;

SET search_path TO private;

-- How a control plane learns who its reviewers are without anyone logging in
-- (ADR-0019). The identity provider either pushes users and groups over SCIM,
-- or the control plane pulls them from Google Workspace, Auth0 or Cognito.
-- Both land in users and user_groups, which is what every approval reads;
-- these tables only record where a row came from and how to reach the source.

-- One SCIM bearer token per organization. Only its hash is stored, like
-- sidecars.key_hash: the plain token is shown once, when it is generated.
CREATE TABLE IF NOT EXISTS scim_tokens (
    org_id       UUID PRIMARY KEY REFERENCES orgs(id) ON DELETE CASCADE,
    token_hash   TEXT NOT NULL UNIQUE,
    created_by   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMPTZ NULL
);

-- One directory sync per organization. settings holds the provider's
-- credentials, as server_auth_config holds the OIDC client secret; the API
-- never returns them in the clear. group_ids are the provider's own ids of the
-- groups whose members are synced.
CREATE TABLE IF NOT EXISTS directory_sync_configs (
    org_id           UUID PRIMARY KEY REFERENCES orgs(id) ON DELETE CASCADE,
    provider         VARCHAR(32) NOT NULL CHECK (provider IN ('google', 'auth0', 'cognito')),
    settings         JSONB NOT NULL DEFAULT '{}',
    group_ids        TEXT[] NOT NULL DEFAULT '{}',
    interval_minutes INTEGER NOT NULL DEFAULT 15 CHECK (interval_minutes >= 5),
    last_run_at      TIMESTAMPTZ NULL,
    last_error       TEXT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The users the identity provider describes. source says which path wrote the
-- row: scim, google, auth0 or cognito. external_id is the provider's id, which
-- is how a sync finds the same person again after an email change.
CREATE TABLE IF NOT EXISTS directory_users (
    user_id     UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    org_id      UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    source      VARCHAR(32) NOT NULL,
    external_id TEXT NULL,
    user_name   TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_directory_users_external
    ON directory_users (org_id, source, external_id) WHERE external_id IS NOT NULL;

-- The groups the identity provider describes. display_name is the name a
-- rule's reviewers_groups carries and the name user_groups stores; the id is
-- what SCIM addresses a group by, so a rename keeps the same row.
CREATE TABLE IF NOT EXISTS directory_groups (
    id           UUID PRIMARY KEY,
    org_id       UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    source       VARCHAR(32) NOT NULL,
    display_name VARCHAR(100) NOT NULL,
    external_id  TEXT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, display_name)
);

COMMIT;
