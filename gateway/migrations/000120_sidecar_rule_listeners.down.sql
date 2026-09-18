BEGIN;

SET search_path TO private;

DROP TABLE IF EXISTS ai_session_analyzer_rules_listeners;
DROP TABLE IF EXISTS datamasking_rules_listeners;
DROP TABLE IF EXISTS guardrail_rules_listeners;

COMMIT;
