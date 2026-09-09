BEGIN;

SET search_path TO private;

DROP INDEX IF EXISTS idx_connections_opa_config_id;
ALTER TABLE connections DROP COLUMN IF EXISTS opa_config_id;
DROP TABLE IF EXISTS opa_configs;

DROP INDEX IF EXISTS idx_connections_sidecar_id;
ALTER TABLE connections DROP COLUMN IF EXISTS sidecar_id;

-- Sidecars carry the daemon config the control plane serves them.
ALTER TABLE sidecars ADD COLUMN IF NOT EXISTS configuration JSONB NOT NULL DEFAULT '{}'::jsonb;

COMMIT;
