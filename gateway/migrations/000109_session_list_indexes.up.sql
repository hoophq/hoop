BEGIN;
SET search_path TO private;

CREATE INDEX IF NOT EXISTS index_sessions_org_created_at
    ON sessions (org_id, created_at DESC, id DESC);

COMMIT;
