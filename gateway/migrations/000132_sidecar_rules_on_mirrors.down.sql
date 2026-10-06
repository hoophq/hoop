BEGIN;

SET search_path TO private;

-- Every binding on a mirror goes back to its listener, where the code before
-- 000132 reads it.
INSERT INTO guardrail_rules_listeners (org_id, guardrail_rule_name, sidecar_id, listener_name, position)
SELECT r.org_id, r.name, c.sidecar_id, c.sidecar_listener, g.position
FROM guardrail_rules_connections g
JOIN guardrail_rules r ON r.id = g.rule_id
JOIN connections c ON c.id = g.connection_id
WHERE c.sidecar_id IS NOT NULL
ON CONFLICT DO NOTHING;
DELETE FROM guardrail_rules_connections g USING connections c
WHERE c.id = g.connection_id AND c.sidecar_id IS NOT NULL;

INSERT INTO datamasking_rules_listeners (org_id, datamasking_rule_name, sidecar_id, listener_name, position)
SELECT r.org_id, r.name, c.sidecar_id, c.sidecar_listener, d.position
FROM datamasking_rules_connections d
JOIN datamasking_rules r ON r.id = d.rule_id
JOIN connections c ON c.id = d.connection_id
WHERE c.sidecar_id IS NOT NULL
ON CONFLICT DO NOTHING;
DELETE FROM datamasking_rules_connections d USING connections c
WHERE c.id = d.connection_id AND c.sidecar_id IS NOT NULL;

INSERT INTO ai_session_analyzer_rules_listeners (org_id, analyzer_rule_name, sidecar_id, listener_name, position)
SELECT a.org_id, a.analyzer_rule_name, c.sidecar_id, c.sidecar_listener, a.position
FROM ai_session_analyzer_rules_connections a
JOIN connections c ON c.id = a.connection_id
WHERE c.sidecar_id IS NOT NULL
ON CONFLICT DO NOTHING;

DROP TABLE ai_session_analyzer_rules_connections;

COMMIT;
