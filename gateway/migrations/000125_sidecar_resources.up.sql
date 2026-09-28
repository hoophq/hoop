BEGIN;

SET search_path TO private;

-- ADR-0022 prototype, behind experimental.sidecar_resources. A sidecar is
-- stored as a resource and each of its listeners as a role (a connections
-- row). The UI, the handshake and the served document still speak sidecar
-- and listener; sidecars.configuration stays the source of what is served.

-- Who owns a resource row. 'sidecar' rows are written by the control plane
-- from a sidecar's configuration and are never adopted from another owner.
ALTER TABLE resources ADD COLUMN IF NOT EXISTS managed_by VARCHAR(100) NULL;

-- The resource a sidecar is stored as.
ALTER TABLE sidecars ADD COLUMN IF NOT EXISTS resource_name VARCHAR(128) NULL;
ALTER TABLE sidecars ADD CONSTRAINT sidecars_resource_fk
    FOREIGN KEY (org_id, resource_name) REFERENCES resources (org_id, name)
    ON UPDATE CASCADE;

-- The role a listener is stored as. A real foreign key on both sides, unlike
-- the listener_name columns that key on an element of the JSON document.
CREATE TABLE IF NOT EXISTS sidecar_listener_roles (
    org_id        UUID NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    sidecar_id    UUID NOT NULL REFERENCES sidecars (id) ON DELETE CASCADE,
    listener_name VARCHAR(255) NOT NULL CHECK (listener_name <> ''),
    connection_id UUID NOT NULL REFERENCES connections (id) ON DELETE CASCADE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (sidecar_id, listener_name),
    UNIQUE (connection_id)
);

COMMIT;
