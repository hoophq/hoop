BEGIN;

SET search_path TO private;

DROP TABLE IF EXISTS runbooks;
DROP TABLE IF EXISTS runbook_rules;

COMMIT;
