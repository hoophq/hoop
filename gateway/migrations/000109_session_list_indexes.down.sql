BEGIN;
SET search_path TO private;

DROP INDEX IF EXISTS index_sessions_org_created_at;

COMMIT;
