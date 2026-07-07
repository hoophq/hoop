BEGIN;

SET search_path TO private;

ALTER TABLE ai_session_analyzer_rules
    DROP COLUMN IF EXISTS custom_prompt;

COMMIT;
