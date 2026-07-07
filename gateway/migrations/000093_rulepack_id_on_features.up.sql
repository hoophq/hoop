BEGIN;

SET search_path TO private;

ALTER TABLE datamasking_rules
    ADD COLUMN rulepack_id UUID NULL REFERENCES rulepacks(id) ON DELETE CASCADE ON UPDATE CASCADE;
CREATE INDEX idx_datamasking_rules_rulepack_id ON datamasking_rules(rulepack_id);

ALTER TABLE guardrail_rules
    ADD COLUMN rulepack_id UUID NULL REFERENCES rulepacks(id) ON DELETE CASCADE ON UPDATE CASCADE;
CREATE INDEX idx_guardrail_rules_rulepack_id ON guardrail_rules(rulepack_id);

COMMIT;
