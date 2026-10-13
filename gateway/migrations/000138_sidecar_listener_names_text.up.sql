BEGIN;

SET search_path TO private;

-- The gateway bounds a listener name in bytes (MaxSidecarListenerNameBytes),
-- what the indexes hold. VARCHAR(255) to TEXT rewrites no table and no index.
ALTER TABLE connections ALTER COLUMN sidecar_listener TYPE TEXT;
ALTER TABLE reviews ALTER COLUMN listener_name TYPE TEXT;
ALTER TABLE guardrail_rules_listeners ALTER COLUMN listener_name TYPE TEXT;
ALTER TABLE datamasking_rules_listeners ALTER COLUMN listener_name TYPE TEXT;
ALTER TABLE ai_session_analyzer_rules_listeners ALTER COLUMN listener_name TYPE TEXT;
ALTER TABLE sidecar_slack_channels ALTER COLUMN listener_name TYPE TEXT;

COMMIT;
