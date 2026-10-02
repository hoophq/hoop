BEGIN;

SET search_path TO private;

DROP TRIGGER IF EXISTS orgs_license_sidecar_config_gen ON orgs;
DROP TRIGGER IF EXISTS sidecars_sidecar_config_gen ON sidecars;
DROP TRIGGER IF EXISTS ai_session_analyzer_rules_listeners_sidecar_config_gen ON ai_session_analyzer_rules_listeners;
DROP TRIGGER IF EXISTS ai_session_analyzer_rules_sidecar_config_gen ON ai_session_analyzer_rules;
DROP TRIGGER IF EXISTS datamasking_rules_listeners_sidecar_config_gen ON datamasking_rules_listeners;
DROP TRIGGER IF EXISTS datamasking_rules_sidecar_config_gen ON datamasking_rules;
DROP TRIGGER IF EXISTS guardrail_rules_listeners_sidecar_config_gen ON guardrail_rules_listeners;
DROP TRIGGER IF EXISTS guardrail_rules_sidecar_config_gen ON guardrail_rules;
DROP FUNCTION IF EXISTS bump_sidecar_config_gen();

ALTER TABLE sidecars
    DROP COLUMN IF EXISTS served_gen,
    DROP COLUMN IF EXISTS composed_at;
DROP TABLE IF EXISTS sidecar_config_gens;

COMMIT;
