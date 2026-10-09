BEGIN;

SET search_path TO private;

-- A stored name over 255 characters stops the rollback ("value too long"):
-- Postgres refuses it, never cuts it. Rename that listener first.
ALTER TABLE sidecar_slack_channels ALTER COLUMN listener_name TYPE VARCHAR(255);
ALTER TABLE ai_session_analyzer_rules_listeners ALTER COLUMN listener_name TYPE VARCHAR(255);
ALTER TABLE datamasking_rules_listeners ALTER COLUMN listener_name TYPE VARCHAR(255);
ALTER TABLE guardrail_rules_listeners ALTER COLUMN listener_name TYPE VARCHAR(255);
ALTER TABLE reviews ALTER COLUMN listener_name TYPE VARCHAR(255);
ALTER TABLE connections ALTER COLUMN sidecar_listener TYPE VARCHAR(255);

COMMIT;
