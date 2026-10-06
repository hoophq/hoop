BEGIN;

SET search_path TO private;

-- A sidecar rule binding gets a copy on the listener's mirror connection
-- (000128), the resource the admin sees. Expand phase: *_rules_listeners stays
-- the source of truth and keeps every row. A gateway older than this migration
-- reads and writes only those rows, during a rolling deploy or after an image
-- rollback. services.SyncSidecarListenerConnectionsTx keeps the copies in step,
-- and makes them for a mirror that comes later.

-- The analyzer has no connection junction, only connection_names. Keyed by
-- the rule name as the listener and attribute junctions are, so a rename
-- follows it.
CREATE TABLE ai_session_analyzer_rules_connections (
    org_id             UUID NOT NULL,
    analyzer_rule_name VARCHAR(255) NOT NULL,
    connection_id      UUID NOT NULL REFERENCES connections(id) ON DELETE CASCADE,
    position           INT NOT NULL DEFAULT 0,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (org_id, analyzer_rule_name, connection_id),
    FOREIGN KEY (org_id, analyzer_rule_name)
        REFERENCES ai_session_analyzer_rules(org_id, name) ON UPDATE CASCADE ON DELETE CASCADE
);
CREATE INDEX idx_analyzer_rules_connections_connection
    ON ai_session_analyzer_rules_connections (connection_id);

-- A row on a mirror written through a rule's connection list protects
-- nothing: no agent session runs on a mirror, and no sidecar read it. From
-- here on a row on a mirror is a sidecar binding, so one that skipped the
-- sidecar write checks must not stay.
DELETE FROM guardrail_rules_connections g USING connections c
WHERE c.id = g.connection_id AND c.sidecar_id IS NOT NULL;
DELETE FROM datamasking_rules_connections d USING connections c
WHERE c.id = d.connection_id AND c.sidecar_id IS NOT NULL;

-- The copy. The position is kept: the sidecar evaluates rules in order.
INSERT INTO guardrail_rules_connections (org_id, rule_id, connection_id, position)
SELECT b.org_id, r.id, c.id, b.position
FROM guardrail_rules_listeners b
JOIN guardrail_rules r ON r.org_id = b.org_id AND r.name = b.guardrail_rule_name
JOIN connections c ON c.org_id = b.org_id AND c.sidecar_id = b.sidecar_id AND c.sidecar_listener = b.listener_name;

INSERT INTO datamasking_rules_connections (org_id, rule_id, connection_id, position)
SELECT b.org_id, r.id, c.id, b.position
FROM datamasking_rules_listeners b
JOIN datamasking_rules r ON r.org_id = b.org_id AND r.name = b.datamasking_rule_name
JOIN connections c ON c.org_id = b.org_id AND c.sidecar_id = b.sidecar_id AND c.sidecar_listener = b.listener_name;

INSERT INTO ai_session_analyzer_rules_connections (org_id, analyzer_rule_name, connection_id, position)
SELECT b.org_id, b.analyzer_rule_name, c.id, b.position
FROM ai_session_analyzer_rules_listeners b
JOIN connections c ON c.org_id = b.org_id AND c.sidecar_id = b.sidecar_id AND c.sidecar_listener = b.listener_name;

COMMIT;
