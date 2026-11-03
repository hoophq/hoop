BEGIN;

SET search_path TO private;

-- A sidecar asking to join an organization by device code. The sidecar makes
-- its own token and sends it once; only the hash is kept, so approving a row
-- creates a sidecar that token already reaches. org_id stays NULL until an
-- admin approves, because the request carries no organization.
CREATE TABLE IF NOT EXISTS sidecar_logins (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_code      VARCHAR(16) NOT NULL,
    key_hash       VARCHAR(128) NOT NULL,
    requested_name VARCHAR(255) NOT NULL,
    status         VARCHAR(16) NOT NULL DEFAULT 'pending'
                   CHECK (status IN ('pending', 'approved', 'denied')),
    org_id         UUID NULL REFERENCES orgs(id) ON DELETE CASCADE,
    sidecar_id     UUID NULL REFERENCES sidecars(id) ON DELETE SET NULL,
    decided_by     VARCHAR(255) NULL,
    expires_at     TIMESTAMPTZ NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_sidecar_logins_user_code ON sidecar_logins (user_code);
CREATE UNIQUE INDEX IF NOT EXISTS idx_sidecar_logins_key_hash ON sidecar_logins (key_hash);
CREATE INDEX IF NOT EXISTS idx_sidecar_logins_expires_at ON sidecar_logins (expires_at);

COMMIT;
