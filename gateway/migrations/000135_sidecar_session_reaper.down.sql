BEGIN;
SET search_path TO private;

DROP INDEX IF EXISTS index_sessions_open_sidecar;

COMMIT;
