BEGIN;

SET search_path TO private;

DROP INDEX IF EXISTS idx_scim_groups_org_external_id;
DROP TABLE IF EXISTS scim_groups;
DROP INDEX IF EXISTS idx_users_org_external_id;
ALTER TABLE users DROP COLUMN IF EXISTS external_id;
DROP TABLE IF EXISTS scim_tokens;

COMMIT;
