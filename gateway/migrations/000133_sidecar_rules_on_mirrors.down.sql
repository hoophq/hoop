BEGIN;

SET search_path TO private;

-- The listener rows hold every binding: 000133 keeps them and the gateway
-- writes them on every change. Only the copies on the mirrors go.
DELETE FROM guardrail_rules_connections g USING connections c
WHERE c.id = g.connection_id AND c.sidecar_id IS NOT NULL;
DELETE FROM datamasking_rules_connections d USING connections c
WHERE c.id = d.connection_id AND c.sidecar_id IS NOT NULL;

DROP TABLE ai_session_analyzer_rules_connections;

COMMIT;
