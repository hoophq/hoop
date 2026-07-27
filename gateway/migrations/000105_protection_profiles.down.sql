BEGIN;
SET search_path TO private;

DROP TABLE IF EXISTS ai_session_analyzer_rules_attributes;

ALTER TABLE ai_session_analyzer_rules DROP COLUMN IF EXISTS managed_by;
ALTER TABLE access_request_rules      DROP COLUMN IF EXISTS managed_by;
ALTER TABLE datamasking_rules         DROP COLUMN IF EXISTS managed_by;
ALTER TABLE guardrail_rules           DROP COLUMN IF EXISTS managed_by;
ALTER TABLE attributes                DROP COLUMN IF EXISTS managed_by;

ALTER TABLE orgs DROP COLUMN IF EXISTS default_protection_profile;

COMMIT;
