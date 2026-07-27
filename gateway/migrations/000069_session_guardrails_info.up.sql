BEGIN;

SET search_path TO private;

ALTER TABLE sessions ADD COLUMN guardrails_info jsonb NULL;

COMMIT;