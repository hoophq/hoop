BEGIN;

SET search_path TO private;

DROP TABLE IF EXISTS guardrail_rules;
DROP TABLE IF EXISTS guardrail_rules_connections;

COMMIT;