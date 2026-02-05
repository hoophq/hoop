BEGIN;

SET search_path TO private;

ALTER TABLE connections DROP CONSTRAINT IF EXISTS connection_access_control_rule_fk;
ALTER TABLE connections DROP COLUMN IF EXISTS access_control_rule_name;

DROP TABLE access_control_rules;

COMMIT;
