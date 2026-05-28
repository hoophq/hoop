BEGIN;

SET search_path TO private;

DROP INDEX IF EXISTS idx_attributes_rulepack_id;
ALTER TABLE attributes DROP COLUMN IF EXISTS rulepack_id;

DROP TABLE IF EXISTS rulepacks;

COMMIT;
