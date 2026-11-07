BEGIN;

SET search_path TO private;

ALTER TABLE sidecars DROP COLUMN IF EXISTS heartbeat_seconds;

COMMIT;
