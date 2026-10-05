BEGIN;

SET search_path TO private;

-- A connection that mirrors one listener of a sidecar. 000116 dropped the
-- first sidecar_id; this one cascades, because the mirror has no life of its
-- own: the sidecar owns it and deleting the sidecar deletes it.
ALTER TABLE connections
    ADD COLUMN sidecar_id UUID NULL
        CONSTRAINT connections_sidecar_id_fkey REFERENCES sidecars(id) ON DELETE CASCADE,
    ADD COLUMN sidecar_listener VARCHAR(255) NULL;

-- Both set or both null. Not NOT VALID: a row that breaks this must stop the
-- migration, never be rewritten.
ALTER TABLE connections ADD CONSTRAINT connections_sidecar_listener_check
    CHECK ((sidecar_id IS NULL) = (sidecar_listener IS NULL));

CREATE UNIQUE INDEX idx_connections_sidecar_listener
    ON connections (org_id, sidecar_id, sidecar_listener)
    WHERE sidecar_id IS NOT NULL;

-- A rule's place in its connection, as 000122 gave the listener junctions.
-- The sidecar evaluates rules in order, so the order is policy, and the move
-- from *_rules_listeners must keep it.
ALTER TABLE guardrail_rules_connections ADD COLUMN position INT NOT NULL DEFAULT 0;
ALTER TABLE datamasking_rules_connections ADD COLUMN position INT NOT NULL DEFAULT 0;

COMMIT;
