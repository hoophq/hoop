BEGIN;
SET search_path TO private;

DROP INDEX IF EXISTS idx_connections_org_resource_name;

COMMIT;
