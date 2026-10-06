BEGIN;

SET search_path TO private;

-- The listener rows hold every binding: 000133 keeps them and the gateway
-- writes them on every change. Only the copies on the mirrors go.
DROP TABLE ai_session_analyzer_rules_mirrors;
DROP TABLE datamasking_rules_mirrors;
DROP TABLE guardrail_rules_mirrors;

COMMIT;
