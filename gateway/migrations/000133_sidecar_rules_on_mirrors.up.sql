BEGIN;

SET search_path TO private;

-- A sidecar rule binding gets a copy on the listener's mirror connection
-- (000128), the resource the admin sees. Expand phase: *_rules_listeners stays
-- the source of truth and keeps every row. A gateway older than this migration
-- reads and writes only those rows, during a rolling deploy or after an image
-- rollback. services.SyncSidecarListenerConnectionsTx keeps the copies in step,
-- and makes them for a mirror that comes later.
--
-- Own tables, not the *_rules_connections junctions: those carry a rule's
-- connection_ids, a public API field, and must keep their meaning.
-- Keyed by the rule name, as the listener and attribute junctions are, so a
-- rename follows it.

CREATE TABLE guardrail_rules_mirrors (
    org_id              UUID NOT NULL,
    guardrail_rule_name VARCHAR(254) NOT NULL,
    connection_id       UUID NOT NULL REFERENCES connections(id) ON DELETE CASCADE,
    position            INT NOT NULL DEFAULT 0,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (org_id, guardrail_rule_name, connection_id),
    FOREIGN KEY (org_id, guardrail_rule_name)
        REFERENCES guardrail_rules(org_id, name) ON UPDATE CASCADE ON DELETE CASCADE
);
CREATE INDEX idx_guardrail_rules_mirrors_connection ON guardrail_rules_mirrors (connection_id);

CREATE TABLE datamasking_rules_mirrors (
    org_id                UUID NOT NULL,
    datamasking_rule_name VARCHAR(254) NOT NULL,
    connection_id         UUID NOT NULL REFERENCES connections(id) ON DELETE CASCADE,
    position              INT NOT NULL DEFAULT 0,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (org_id, datamasking_rule_name, connection_id),
    FOREIGN KEY (org_id, datamasking_rule_name)
        REFERENCES datamasking_rules(org_id, name) ON UPDATE CASCADE ON DELETE CASCADE
);
CREATE INDEX idx_datamasking_rules_mirrors_connection ON datamasking_rules_mirrors (connection_id);

CREATE TABLE ai_session_analyzer_rules_mirrors (
    org_id             UUID NOT NULL,
    analyzer_rule_name VARCHAR(254) NOT NULL,
    connection_id      UUID NOT NULL REFERENCES connections(id) ON DELETE CASCADE,
    position           INT NOT NULL DEFAULT 0,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (org_id, analyzer_rule_name, connection_id),
    FOREIGN KEY (org_id, analyzer_rule_name)
        REFERENCES ai_session_analyzer_rules(org_id, name) ON UPDATE CASCADE ON DELETE CASCADE
);
CREATE INDEX idx_analyzer_rules_mirrors_connection ON ai_session_analyzer_rules_mirrors (connection_id);

-- The copy. The position is kept: the sidecar evaluates rules in order.
INSERT INTO guardrail_rules_mirrors (org_id, guardrail_rule_name, connection_id, position)
SELECT b.org_id, b.guardrail_rule_name, c.id, b.position
FROM guardrail_rules_listeners b
JOIN connections c ON c.org_id = b.org_id AND c.sidecar_id = b.sidecar_id AND c.sidecar_listener = b.listener_name;

INSERT INTO datamasking_rules_mirrors (org_id, datamasking_rule_name, connection_id, position)
SELECT b.org_id, b.datamasking_rule_name, c.id, b.position
FROM datamasking_rules_listeners b
JOIN connections c ON c.org_id = b.org_id AND c.sidecar_id = b.sidecar_id AND c.sidecar_listener = b.listener_name;

INSERT INTO ai_session_analyzer_rules_mirrors (org_id, analyzer_rule_name, connection_id, position)
SELECT b.org_id, b.analyzer_rule_name, c.id, b.position
FROM ai_session_analyzer_rules_listeners b
JOIN connections c ON c.org_id = b.org_id AND c.sidecar_id = b.sidecar_id AND c.sidecar_listener = b.listener_name;

COMMIT;
