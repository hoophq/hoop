BEGIN;

SET search_path TO private;

-- How a control plane learns who its reviewers are without anyone logging in
-- (ADR-0020): it imports the members of Slack user groups into users and
-- user_groups, which is what every approval reads. users.slack_id links a
-- user to Slack. No secret is stored: the import reads Slack through the
-- org's Slack app.

-- One Slack import per organization. group_ids are the Slack ids of the user
-- groups whose members are imported. allow_member_managed_groups accepts
-- groups a workspace member who is not an admin edited last.
CREATE TABLE IF NOT EXISTS directory_sync_configs (
    org_id                      UUID PRIMARY KEY REFERENCES orgs(id) ON DELETE CASCADE,
    group_ids                   TEXT[] NOT NULL DEFAULT '{}',
    interval_minutes            INTEGER NOT NULL DEFAULT 15 CHECK (interval_minutes >= 5),
    allow_member_managed_groups BOOLEAN NOT NULL DEFAULT FALSE,
    last_run_at                 TIMESTAMPTZ NULL,
    last_error                  TEXT NULL,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The hoop groups the import owns, one per Slack user group. external_id is
-- the Slack user group id. display_name is the hoop group name: the handle at
-- the first import. It does not follow a rename in Slack, so the rules that
-- name it keep working. It is as wide as user_groups.name.
CREATE TABLE IF NOT EXISTS directory_groups (
    org_id       UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    external_id  TEXT NOT NULL,
    display_name VARCHAR(100) NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (org_id, external_id),
    UNIQUE (org_id, display_name)
);

COMMIT;
