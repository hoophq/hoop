BEGIN;

SET search_path TO private;

DROP INDEX IF EXISTS idx_conn_cred_sidecar_lane;

COMMIT;
