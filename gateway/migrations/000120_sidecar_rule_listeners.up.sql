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
-- A rule binds to ONE LISTENER, never to a sidecar as a whole. Binding to the
-- sidecar would write the document's top-level block, which every lane
-- inherits -- and a lane that also carries its own mask block REPLACES it,
-- so the same rule would apply to some lanes and be ignored on others with
-- nothing in the UI saying which. The listener is also what carries the
-- protocol, and the protocol is what decides which rule types and which
-- masking strategies are even legal (an ssh lane refuses `table` rules and
-- every masking strategy but `mask`). There is no useful rule you can write
-- without knowing it.
--
-- The CHECK is what holds that up. NOT NULL alone would still admit '', and
-- '' is exactly the sidecar-wide binding this refuses. A future
-- attribute-keyed binding is still additive: it adds a column, and drops
-- this constraint deliberately rather than inheriting a loophole.

CREATE TABLE IF NOT EXISTS guardrail_rules_listeners (
    org_id              UUID NOT NULL,
    guardrail_rule_name VARCHAR(254) NOT NULL,
    sidecar_id          UUID NOT NULL,
    listener_name       VARCHAR(255) NOT NULL CHECK (listener_name <> ''),
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
    listener_name         VARCHAR(255) NOT NULL CHECK (listener_name <> ''),
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
    listener_name     VARCHAR(255) NOT NULL CHECK (listener_name <> ''),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (org_id, analyzer_rule_name, sidecar_id, listener_name),
    FOREIGN KEY (org_id, analyzer_rule_name)
        REFERENCES ai_session_analyzer_rules(org_id, name) ON UPDATE CASCADE ON DELETE CASCADE,
    FOREIGN KEY (sidecar_id) REFERENCES sidecars(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_analyzer_rules_listeners_sidecar
    ON ai_session_analyzer_rules_listeners (org_id, sidecar_id);

COMMIT;
