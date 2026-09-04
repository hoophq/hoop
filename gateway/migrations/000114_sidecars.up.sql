BEGIN;

SET search_path TO private;

CREATE TABLE IF NOT EXISTS sidecars (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES orgs(id),
    name VARCHAR(255) NOT NULL,
    key_hash VARCHAR(64) NOT NULL,
    created_by VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_sidecars_org_name ON sidecars(org_id, name);
CREATE UNIQUE INDEX idx_sidecars_key_hash ON sidecars(key_hash);

ALTER TABLE connections ADD COLUMN sidecar_id UUID NULL
    REFERENCES sidecars(id) ON DELETE SET NULL;
CREATE INDEX idx_connections_sidecar_id ON connections(sidecar_id)
    WHERE sidecar_id IS NOT NULL;

COMMIT;
