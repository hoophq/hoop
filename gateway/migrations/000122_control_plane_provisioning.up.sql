BEGIN;

SET search_path TO private;

-- How a control plane learns who its reviewers are without anyone logging in
-- (ADR-0019). The control plane pulls Slack user groups, or an admin imports a
-- file. Both land in users and user_groups, which is what every approval
-- reads; these tables only record where a row came from. No secret is stored.

-- One directory sync per organization. It reads Slack through the org's Slack
-- app, so it holds no credential. group_ids are the Slack ids of the user
-- groups whose members are synced. allow_member_managed_groups accepts groups
-- a workspace member who is not an admin edited last.
CREATE TABLE IF NOT EXISTS directory_sync_configs (
    org_id                      UUID PRIMARY KEY REFERENCES orgs(id) ON DELETE CASCADE,
    provider                    VARCHAR(32) NOT NULL CHECK (provider IN ('slack')),
    group_ids                   TEXT[] NOT NULL DEFAULT '{}',
    interval_minutes            INTEGER NOT NULL DEFAULT 15 CHECK (interval_minutes >= 5),
    allow_member_managed_groups BOOLEAN NOT NULL DEFAULT FALSE,
    last_run_at                 TIMESTAMPTZ NULL,
    last_error                  TEXT NULL,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The users a source describes. source says which path wrote the row: slack
-- or file. external_id is the source's id, which is how the next run finds the
-- same person again after an email change.
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

-- The groups a source describes. display_name is the name a rule's
-- reviewers_groups carries and the name user_groups stores, so it is as wide
-- as user_groups.name. external_id is the source's id, so a rename keeps the
-- same row.
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
