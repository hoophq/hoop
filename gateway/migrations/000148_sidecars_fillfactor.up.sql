BEGIN;

SET search_path TO private;

-- Every sidecar rewrites its row once a minute (last_seen_at, the handshake
-- columns). None of those columns is indexed, so the update is HOT while the
-- page has room; at the default fillfactor the pages fill and every update
-- then touches both indexes. Leave room, and let autovacuum reclaim earlier.
ALTER TABLE sidecars SET (fillfactor = 70, autovacuum_vacuum_scale_factor = 0.05);

COMMIT;
