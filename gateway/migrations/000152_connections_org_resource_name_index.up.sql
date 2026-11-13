-- Each sidecar mirror write reads connections by (org_id, resource_name).
--
-- A plain, blocking CREATE INDEX, as in 000141: a failed CONCURRENTLY build
-- leaves an INVALID index and a dirty schema_migrations row. The build took
-- 161 ms on 150,000 connections.
BEGIN;
SET search_path TO private;

CREATE INDEX IF NOT EXISTS idx_connections_org_resource_name
    ON connections (org_id, resource_name);

COMMIT;
