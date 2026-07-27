BEGIN;

SET search_path TO private;

DROP TABLE IF EXISTS ai_providers;
DROP TABLE IF EXISTS ai_session_analyzer_rules;

ALTER TABLE sessions DROP COLUMN ai_analysis;

COMMIT;
