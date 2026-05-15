BEGIN;

SET search_path TO private;

DROP INDEX IF EXISTS idx_machine_identities_rulepack_id;
ALTER TABLE machine_identities DROP COLUMN IF EXISTS rulepack_id;

DROP INDEX IF EXISTS idx_access_request_rules_rulepack_id;
ALTER TABLE access_request_rules DROP COLUMN IF EXISTS rulepack_id;

DROP INDEX IF EXISTS idx_guardrail_rules_rulepack_id;
ALTER TABLE guardrail_rules DROP COLUMN IF EXISTS rulepack_id;

DROP INDEX IF EXISTS idx_datamasking_rules_rulepack_id;
ALTER TABLE datamasking_rules DROP COLUMN IF EXISTS rulepack_id;

DROP INDEX IF EXISTS idx_ai_session_analyzer_rules_rulepack_id;
ALTER TABLE ai_session_analyzer_rules DROP COLUMN IF EXISTS rulepack_id;

COMMIT;
