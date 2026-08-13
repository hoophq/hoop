BEGIN;
SET search_path TO private;

-- When true, the analyzer runs an agentic tool-calling loop over past sessions
-- and resource metadata before classifying. Default false preserves the
-- existing single-shot behavior.
ALTER TABLE ai_session_analyzer_rules ADD COLUMN IF NOT EXISTS agentic BOOLEAN NOT NULL DEFAULT false;

COMMIT;
