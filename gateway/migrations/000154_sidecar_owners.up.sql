BEGIN;

SET search_path TO private;

-- The people responsible for one sidecar, in the order they were given.
-- email is not a foreign key: a person becomes a user at the first login,
-- and an owner can be set before that.
CREATE TABLE IF NOT EXISTS sidecar_owners (
    org_id     UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    sidecar_id UUID NOT NULL REFERENCES sidecars(id) ON DELETE CASCADE,
    email      VARCHAR(255) NOT NULL CHECK (email <> '' AND email = lower(email)),
    position   INT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (org_id, sidecar_id, email)
);

COMMIT;
