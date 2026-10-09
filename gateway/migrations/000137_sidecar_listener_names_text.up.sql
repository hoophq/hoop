BEGIN;

SET search_path TO private;

-- The sidecar sets no limit on a listener name, so these columns set none.
-- VARCHAR(255) to TEXT is binary compatible: no table rewrite, no index rebuild.
ALTER TABLE connections ALTER COLUMN sidecar_listener TYPE TEXT;
ALTER TABLE reviews ALTER COLUMN listener_name TYPE TEXT;
ALTER TABLE guardrail_rules_listeners ALTER COLUMN listener_name TYPE TEXT;
ALTER TABLE datamasking_rules_listeners ALTER COLUMN listener_name TYPE TEXT;
ALTER TABLE ai_session_analyzer_rules_listeners ALTER COLUMN listener_name TYPE TEXT;
ALTER TABLE sidecar_slack_channels ALTER COLUMN listener_name TYPE TEXT;

COMMIT;
