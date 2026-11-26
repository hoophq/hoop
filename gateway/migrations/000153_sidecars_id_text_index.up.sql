-- The stale sidecar session reaper finds a session's sidecar by
-- sc.id::text = metadata->'sidecar'->>'id'. The cast hides the primary key,
-- so each open session read every sidecar: 5.6 s a pass with 28,000 open
-- sessions and 3,000 sidecars. This index matches the expression. id never
-- changes, so the heartbeat updates stay HOT (000148).
--
-- A plain, blocking CREATE INDEX, as in 000141: a failed CONCURRENTLY build
-- leaves an INVALID index and a dirty schema_migrations row. sidecars holds
-- one row per sidecar.
BEGIN;
SET search_path TO private;

CREATE INDEX IF NOT EXISTS idx_sidecars_id_text ON sidecars ((id::text));

COMMIT;
