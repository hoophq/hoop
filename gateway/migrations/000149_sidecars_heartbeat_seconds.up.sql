BEGIN;

SET search_path TO private;

-- heartbeat_seconds: the interval the plane told this sidecar to handshake
-- at, recorded with each handshake. NULL for a sidecar the plane never told
-- another interval, which reads as the sidecar's default (60). The mirror
-- sweeper and the session reaper measure a sidecar's silence in multiples of
-- it, so a sidecar told to call less often is not read as gone.
ALTER TABLE sidecars ADD COLUMN IF NOT EXISTS heartbeat_seconds INTEGER NULL;

COMMIT;
