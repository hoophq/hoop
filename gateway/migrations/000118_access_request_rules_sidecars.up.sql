BEGIN;

SET search_path TO private;

-- A sidecar rule targets sidecars, never connections. Every connection lookup
-- matches connection_names, so an empty one keeps a sidecar rule out of them.
ALTER TABLE access_request_rules ADD CONSTRAINT access_request_rules_sidecar_no_connections
    CHECK (access_type <> 'sidecar' OR connection_names = '{}');

-- Targets of the foreign keys below. (org_id, name) is already unique; the
-- first index exists only so a foreign key can include access_type.
CREATE UNIQUE INDEX IF NOT EXISTS idx_access_request_rules_org_name_type ON access_request_rules(org_id, name, access_type);
CREATE UNIQUE INDEX IF NOT EXISTS idx_sidecars_org_id_id ON sidecars(org_id, id);

-- Keyed by sidecar id, not name: a sidecar deleted and created again under the
-- same name has a new token, and an admin must list it again.
--
-- access_type is always 'sidecar' and rides the foreign key. Retyping a rule
-- that lists sidecars cascades into the check and fails for every writer, and
-- a connection rule cannot list a sidecar at all.
CREATE TABLE IF NOT EXISTS access_request_rules_sidecars (
    org_id UUID NOT NULL,
    access_rule_name VARCHAR(254) NOT NULL,
    access_type VARCHAR(16) NOT NULL DEFAULT 'sidecar'
        CONSTRAINT access_request_rules_sidecars_sidecar_only CHECK (access_type = 'sidecar'),
    sidecar_id UUID NOT NULL,
    PRIMARY KEY (org_id, access_rule_name, sidecar_id),
    FOREIGN KEY (org_id, access_rule_name, access_type) REFERENCES access_request_rules(org_id, name, access_type) ON UPDATE CASCADE ON DELETE CASCADE,
    FOREIGN KEY (org_id, sidecar_id) REFERENCES sidecars(org_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_access_request_rules_sidecars_sidecar ON access_request_rules_sidecars(org_id, sidecar_id);

COMMIT;
