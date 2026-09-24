BEGIN;

SET search_path TO private;

-- A rule's place in its listener. The sidecar evaluates rules in order and
-- the first match wins, so the order is policy, not presentation. An imported
-- file keeps its order here; a rule bound by an admin goes last.
ALTER TABLE guardrail_rules_listeners ADD COLUMN IF NOT EXISTS position INT NOT NULL DEFAULT 0;
ALTER TABLE datamasking_rules_listeners ADD COLUMN IF NOT EXISTS position INT NOT NULL DEFAULT 0;
ALTER TABLE ai_session_analyzer_rules_listeners ADD COLUMN IF NOT EXISTS position INT NOT NULL DEFAULT 0;

-- The sidecar whose config file this rule came from. Switching that sidecar
-- back to its file deletes these rules, and only these. NULL is a rule an
-- admin wrote, which the switch only unbinds.
ALTER TABLE guardrail_rules ADD COLUMN IF NOT EXISTS imported_from_sidecar UUID NULL;
ALTER TABLE datamasking_rules ADD COLUMN IF NOT EXISTS imported_from_sidecar UUID NULL;
ALTER TABLE ai_session_analyzer_rules ADD COLUMN IF NOT EXISTS imported_from_sidecar UUID NULL;

COMMIT;
