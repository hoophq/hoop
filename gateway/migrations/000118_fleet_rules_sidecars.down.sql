BEGIN;

SET search_path TO private;

DROP TABLE IF EXISTS ai_session_analyzer_rules_sidecars;
DROP TABLE IF EXISTS datamasking_rules_sidecars;
DROP TABLE IF EXISTS guardrail_rules_sidecars;

COMMIT;
