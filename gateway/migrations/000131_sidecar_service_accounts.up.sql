BEGIN;

SET search_path TO private;

-- Which platform service accounts may reach a sidecar, and which sidecar
-- name each one renders. The sidecar presents the platform's JWT;
-- the plane loads only the rows for its exact issuer before it fetches
-- anything, so an unknown issuer costs no network call.
-- jwks NULL means OIDC discovery at {issuer}/.well-known/openid-configuration.
-- adopt_existing_sidecars lets a matching token bind a sidecar an admin
-- created with a token. Without it, no identity binds such a sidecar.
CREATE TABLE IF NOT EXISTS sidecar_service_accounts (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id            UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name              VARCHAR(255) NOT NULL,
    issuer            TEXT NOT NULL,
    audience          TEXT NOT NULL,
    claim             VARCHAR(16) NOT NULL CHECK (claim IN ('sub', 'email')),
    subject_pattern   TEXT NOT NULL,
    name_template     VARCHAR(255) NOT NULL,
    jwks              JSONB NULL,
    allow_any_subject BOOLEAN NOT NULL DEFAULT FALSE,
    adopt_existing_sidecars BOOLEAN NOT NULL DEFAULT FALSE,
    created_by        VARCHAR(255) NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_sidecar_service_accounts_org_name
    ON sidecar_service_accounts (org_id, name);
CREATE UNIQUE INDEX IF NOT EXISTS idx_sidecar_service_accounts_org_subject
    ON sidecar_service_accounts (org_id, issuer, claim, subject_pattern);
CREATE INDEX IF NOT EXISTS idx_sidecar_service_accounts_issuer
    ON sidecar_service_accounts (issuer);

-- A sidecar a service account created has no token. The identity columns
-- bind a sidecar to the first identity that reached it: another identity is
-- refused until an admin clears them. The lookup stays on (org_id, name). A
-- row may hold neither a token nor an identity: an identity-made sidecar
-- whose binding an admin cleared, which the next identity binds.
ALTER TABLE sidecars
    ALTER COLUMN key_hash DROP NOT NULL,
    ADD COLUMN IF NOT EXISTS identity_issuer TEXT NULL,
    ADD COLUMN IF NOT EXISTS identity_subject TEXT NULL;

-- A name an admin deleted after an identity reached it. Without this row the
-- next heartbeat would create the sidecar again.
CREATE TABLE IF NOT EXISTS sidecar_deleted_names (
    org_id     UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name       VARCHAR(255) NOT NULL,
    deleted_by VARCHAR(255) NOT NULL,
    deleted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (org_id, name)
);

COMMIT;
