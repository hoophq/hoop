BEGIN;

SET search_path TO private;

DROP INDEX IF EXISTS idx_connections_sidecar_id;
ALTER TABLE connections DROP COLUMN IF EXISTS sidecar_id;
DROP TABLE IF EXISTS sidecars;

COMMIT;
