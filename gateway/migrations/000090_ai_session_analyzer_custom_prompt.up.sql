BEGIN;

SET search_path TO private;

ALTER TABLE ai_session_analyzer_rules
    ADD COLUMN custom_prompt TEXT NULL;

COMMIT;
