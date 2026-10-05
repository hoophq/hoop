BEGIN;

SET search_path TO private;

ALTER TABLE datamasking_rules_connections DROP COLUMN position;
ALTER TABLE guardrail_rules_connections DROP COLUMN position;

DROP INDEX idx_connections_sidecar_listener;
ALTER TABLE connections DROP CONSTRAINT connections_sidecar_listener_check;
ALTER TABLE connections
    DROP CONSTRAINT connections_sidecar_id_fkey,
    DROP COLUMN sidecar_listener,
    DROP COLUMN sidecar_id;

COMMIT;
