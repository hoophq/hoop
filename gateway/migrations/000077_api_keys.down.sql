BEGIN;

SET search_path TO private;

DROP INDEX IF EXISTS idx_user_groups_api_key_name;
ALTER TABLE user_groups DROP COLUMN IF EXISTS api_key_id;
DROP TABLE IF EXISTS api_keys;

COMMIT;