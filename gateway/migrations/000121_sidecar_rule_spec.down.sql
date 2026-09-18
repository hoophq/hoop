BEGIN;

SET search_path TO private;

ALTER TABLE guardrail_rules DROP COLUMN IF EXISTS sidecar_spec;
ALTER TABLE datamasking_rules DROP COLUMN IF EXISTS sidecar_spec;
ALTER TABLE ai_session_analyzer_rules DROP COLUMN IF EXISTS sidecar_spec;

COMMIT;
