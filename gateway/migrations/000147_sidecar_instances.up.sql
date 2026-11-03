BEGIN;

SET search_path TO private;

-- One row per process behind a sidecar: pods that share one credential reach
-- one sidecars row, and each names itself by hostname on every request.
CREATE TABLE IF NOT EXISTS sidecar_instances (
    org_id           UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    sidecar_id       UUID NOT NULL REFERENCES sidecars(id) ON DELETE CASCADE,
    name             VARCHAR(253) NOT NULL CHECK (name <> ''),
    reported_version VARCHAR(64) NULL,
    applied_revision VARCHAR(64) NULL,
    last_outcome     VARCHAR(32) NULL,
    last_error       TEXT NULL,
    first_seen_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (sidecar_id, name)
);

COMMIT;
