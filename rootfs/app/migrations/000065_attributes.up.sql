BEGIN;

SET search_path TO private;

CREATE TABLE IF NOT EXISTS attributes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    name VARCHAR(255) NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX idx_attributes_org_name ON attributes(org_id, name);

CREATE TABLE IF NOT EXISTS connections_attributes (
    org_id UUID NOT NULL,
    connection_name VARCHAR(254) NOT NULL,
    attribute_name VARCHAR(255) NOT NULL,
    PRIMARY KEY (org_id, connection_name, attribute_name),
    FOREIGN KEY (org_id, connection_name) REFERENCES connections(org_id, name) ON UPDATE CASCADE ON DELETE CASCADE,
    FOREIGN KEY (org_id, attribute_name) REFERENCES attributes(org_id, name) ON UPDATE CASCADE ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS access_request_rules_attributes (
    org_id UUID NOT NULL,
    access_rule_name VARCHAR(254) NOT NULL,
    attribute_name VARCHAR(255) NOT NULL,
    PRIMARY KEY (org_id, access_rule_name, attribute_name),
    FOREIGN KEY (org_id, access_rule_name) REFERENCES access_request_rules(org_id, name) ON UPDATE CASCADE ON DELETE CASCADE,
    FOREIGN KEY (org_id, attribute_name) REFERENCES attributes(org_id, name) ON UPDATE CASCADE ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS guardrail_rules_attributes (
    org_id UUID NOT NULL,
    guardrail_rule_name VARCHAR(254) NOT NULL,
    attribute_name VARCHAR(255) NOT NULL,
    PRIMARY KEY (org_id, guardrail_rule_name, attribute_name),
    FOREIGN KEY (org_id, guardrail_rule_name) REFERENCES guardrail_rules(org_id, name) ON UPDATE CASCADE ON DELETE CASCADE,
    FOREIGN KEY (org_id, attribute_name) REFERENCES attributes(org_id, name) ON UPDATE CASCADE ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS datamasking_rules_attributes (
    org_id UUID NOT NULL,
    datamasking_rule_name VARCHAR(254) NOT NULL,
    attribute_name VARCHAR(255) NOT NULL,
    PRIMARY KEY (org_id, datamasking_rule_name, attribute_name),
    FOREIGN KEY (org_id, datamasking_rule_name) REFERENCES datamasking_rules(org_id, name) ON UPDATE CASCADE ON DELETE CASCADE,
    FOREIGN KEY (org_id, attribute_name) REFERENCES attributes(org_id, name) ON UPDATE CASCADE ON DELETE CASCADE
);

COMMIT;
