BEGIN;
SET search_path TO private;

-- Org-level default protection profile selected during onboarding (or later
-- from Settings). NULL means manual configuration / never selected.
-- Values are catalog ids owned by the gateway code:
--   hipaa-ready | soc2-type2 | protection-permissive | protection-medium | protection-high
ALTER TABLE orgs ADD COLUMN IF NOT EXISTS default_protection_profile VARCHAR(64) NULL;

-- Ownership marker for rows materialized and lifecycle-managed by Hoop
-- (protection profiles). Managed rows ('hoop') are read-only through the
-- public API and are created/deleted by the profile apply service.
ALTER TABLE attributes                ADD COLUMN IF NOT EXISTS managed_by VARCHAR(32) NULL;
ALTER TABLE guardrail_rules           ADD COLUMN IF NOT EXISTS managed_by VARCHAR(32) NULL;
ALTER TABLE datamasking_rules         ADD COLUMN IF NOT EXISTS managed_by VARCHAR(32) NULL;
ALTER TABLE access_request_rules      ADD COLUMN IF NOT EXISTS managed_by VARCHAR(32) NULL;
ALTER TABLE ai_session_analyzer_rules ADD COLUMN IF NOT EXISTS managed_by VARCHAR(32) NULL;

-- AI session analyzer rules can only target connections by name today.
-- Protection profiles bind rules to a profile attribute instead, so analyzer
-- rules gain the same attribute junction the other rule kinds already have.
CREATE TABLE IF NOT EXISTS ai_session_analyzer_rules_attributes (
    org_id UUID NOT NULL,
    analyzer_rule_name VARCHAR(255) NOT NULL,
    attribute_name VARCHAR(255) NOT NULL,
    PRIMARY KEY (org_id, analyzer_rule_name, attribute_name),
    FOREIGN KEY (org_id, analyzer_rule_name) REFERENCES ai_session_analyzer_rules(org_id, name) ON UPDATE CASCADE ON DELETE CASCADE,
    FOREIGN KEY (org_id, attribute_name) REFERENCES attributes(org_id, name) ON UPDATE CASCADE ON DELETE CASCADE
);

COMMIT;
