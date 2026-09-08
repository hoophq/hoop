BEGIN;

SET search_path TO private;

DROP INDEX IF EXISTS idx_connections_opa_config_id;
ALTER TABLE connections DROP COLUMN IF EXISTS opa_config_id;
DROP TABLE IF EXISTS opa_configs;

COMMIT;
