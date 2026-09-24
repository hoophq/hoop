BEGIN;

SET search_path TO private;

ALTER TABLE sidecars DROP COLUMN IF EXISTS supports_config_reimport;

ALTER TABLE ai_session_analyzer_rules DROP COLUMN IF EXISTS imported_from_sidecar;
ALTER TABLE datamasking_rules DROP COLUMN IF EXISTS imported_from_sidecar;
ALTER TABLE guardrail_rules DROP COLUMN IF EXISTS imported_from_sidecar;

ALTER TABLE ai_session_analyzer_rules_listeners DROP COLUMN IF EXISTS position;
ALTER TABLE datamasking_rules_listeners DROP COLUMN IF EXISTS position;
ALTER TABLE guardrail_rules_listeners DROP COLUMN IF EXISTS position;

COMMIT;
