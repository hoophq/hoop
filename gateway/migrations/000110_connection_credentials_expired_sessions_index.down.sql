BEGIN;
SET search_path TO private;

DROP INDEX IF EXISTS idx_conn_cred_expired_sessions;

COMMIT;
