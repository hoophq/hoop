BEGIN;

SET search_path TO private;

ALTER TABLE sessions DROP COLUMN IF EXISTS guardrails_info;

COMMIT;
