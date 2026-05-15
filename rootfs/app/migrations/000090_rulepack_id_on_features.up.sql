BEGIN;

SET search_path TO private;

ALTER TABLE datamasking_rules
    ADD COLUMN rulepack_id UUID NULL REFERENCES rulepacks(id) ON DELETE CASCADE ON UPDATE CASCADE;
CREATE INDEX idx_datamasking_rules_rulepack_id ON datamasking_rules(rulepack_id);

ALTER TABLE guardrail_rules
    ADD COLUMN rulepack_id UUID NULL REFERENCES rulepacks(id) ON DELETE CASCADE ON UPDATE CASCADE;
CREATE INDEX idx_guardrail_rules_rulepack_id ON guardrail_rules(rulepack_id);

ALTER TABLE access_request_rules
    ADD COLUMN rulepack_id UUID NULL REFERENCES rulepacks(id) ON DELETE CASCADE ON UPDATE CASCADE;
CREATE INDEX idx_access_request_rules_rulepack_id ON access_request_rules(rulepack_id);

ALTER TABLE machine_identities
    ADD COLUMN rulepack_id UUID NULL REFERENCES rulepacks(id) ON DELETE CASCADE ON UPDATE CASCADE;
CREATE INDEX idx_machine_identities_rulepack_id ON machine_identities(rulepack_id);

ALTER TABLE ai_session_analyzer_rules
    ADD COLUMN rulepack_id UUID NULL REFERENCES rulepacks(id) ON DELETE CASCADE ON UPDATE CASCADE;
CREATE INDEX idx_ai_session_analyzer_rules_rulepack_id ON ai_session_analyzer_rules(rulepack_id);

COMMIT;
