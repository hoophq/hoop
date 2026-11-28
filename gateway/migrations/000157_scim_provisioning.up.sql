BEGIN;

SET search_path TO private;

-- Per-organization SCIM 2.0 bearer token (only the SHA-256 hash is stored).
CREATE TABLE IF NOT EXISTS scim_tokens (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     UUID NOT NULL UNIQUE REFERENCES orgs(id) ON DELETE CASCADE,
    token_hash VARCHAR(64) NOT NULL UNIQUE,
    created_by VARCHAR(255) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    rotated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- External directory identifier for SCIM-provisioned users.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS external_id VARCHAR(255) NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_org_external_id
    ON users (org_id, external_id)
    WHERE external_id IS NOT NULL;

-- SCIM 2.0 group directory rows; memberships live in private.user_groups.
CREATE TABLE IF NOT EXISTS scim_groups (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id       UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    external_id  VARCHAR(255) NULL,
    display_name VARCHAR(255) NOT NULL CHECK (display_name <> ''),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, display_name)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_scim_groups_org_external_id
    ON scim_groups (org_id, external_id)
    WHERE external_id IS NOT NULL;

COMMIT;
