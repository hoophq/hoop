BEGIN;

SET search_path TO private;

DROP TABLE IF EXISTS connections_attributes;
DROP TABLE IF EXISTS access_request_rules_attributes;
DROP TABLE IF EXISTS guardrail_rules_attributes;
DROP TABLE IF EXISTS datamasking_rules_attributes;
DROP TABLE IF EXISTS attributes;

COMMIT;
