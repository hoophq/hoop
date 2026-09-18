BEGIN;

SET search_path TO private;

-- Binds a rule to the sidecar listeners that must enforce it.
--
-- The rule rows themselves do not move: guardrail_rules, datamasking_rules and
-- ai_session_analyzer_rules stay exactly as the gateway writes them, and these
-- tables are the sidecar's half of the same binding the *_rules_attributes
-- junctions already provide for connections (000068). Same shape, with
-- sidecar_id + listener_name where those carry connection_name.
--
-- listener_name is NOT a foreign key and cannot be: a listener is an element of
-- sidecars.configuration, not a row. What holds it up is the presence and
-- uniqueness check in services.ValidateListenerNames, which every write to that
-- document passes through, plus the bind-time check that the name appears in
-- the sidecar's stored configuration.
--
-- An empty listener_name targets the WHOLE sidecar. That is not a sentinel for
-- "unset": it selects the document's top-level block, which the daemon
-- concatenates into every lane (Config.resolve). NOT NULL with '' because a
-- nullable column cannot sit in a primary key.

CREATE TABLE IF NOT EXISTS guardrail_rules_listeners (
    org_id              UUID NOT NULL,
    guardrail_rule_name VARCHAR(254) NOT NULL,
    sidecar_id          UUID NOT NULL,
    listener_name       VARCHAR(255) NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (org_id, guardrail_rule_name, sidecar_id, listener_name),
    FOREIGN KEY (org_id, guardrail_rule_name)
        REFERENCES guardrail_rules(org_id, name) ON UPDATE CASCADE ON DELETE CASCADE,
    FOREIGN KEY (sidecar_id) REFERENCES sidecars(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_guardrail_rules_listeners_sidecar
    ON guardrail_rules_listeners (org_id, sidecar_id);

CREATE TABLE IF NOT EXISTS datamasking_rules_listeners (
    org_id                UUID NOT NULL,
    datamasking_rule_name VARCHAR(254) NOT NULL,
    sidecar_id            UUID NOT NULL,
    listener_name         VARCHAR(255) NOT NULL DEFAULT '',
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (org_id, datamasking_rule_name, sidecar_id, listener_name),
    FOREIGN KEY (org_id, datamasking_rule_name)
        REFERENCES datamasking_rules(org_id, name) ON UPDATE CASCADE ON DELETE CASCADE,
    FOREIGN KEY (sidecar_id) REFERENCES sidecars(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_datamasking_rules_listeners_sidecar
    ON datamasking_rules_listeners (org_id, sidecar_id);

CREATE TABLE IF NOT EXISTS ai_session_analyzer_rules_listeners (
    org_id            UUID NOT NULL,
    analyzer_rule_name VARCHAR(254) NOT NULL,
    sidecar_id        UUID NOT NULL,
    listener_name     VARCHAR(255) NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (org_id, analyzer_rule_name, sidecar_id, listener_name),
    FOREIGN KEY (org_id, analyzer_rule_name)
        REFERENCES ai_session_analyzer_rules(org_id, name) ON UPDATE CASCADE ON DELETE CASCADE,
    FOREIGN KEY (sidecar_id) REFERENCES sidecars(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_analyzer_rules_listeners_sidecar
    ON ai_session_analyzer_rules_listeners (org_id, sidecar_id);

COMMIT;
