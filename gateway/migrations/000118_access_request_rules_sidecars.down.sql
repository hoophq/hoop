BEGIN;

SET search_path TO private;

DROP TABLE IF EXISTS access_request_rules_sidecars;
DROP INDEX IF EXISTS idx_sidecars_org_id_id;
DROP INDEX IF EXISTS idx_access_request_rules_org_name_type;
ALTER TABLE access_request_rules DROP CONSTRAINT IF EXISTS access_request_rules_sidecar_no_connections;

COMMIT;
