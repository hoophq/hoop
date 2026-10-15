BEGIN;

SET search_path TO private;

-- reported_configuration is the config file a sidecar runs, as its last
-- handshake reported it. The plane shows it and adopts it only on an admin's
-- action. NULL for a sidecar that never reported one.
ALTER TABLE sidecars
    ADD COLUMN IF NOT EXISTS reported_configuration JSONB NULL,
    ADD COLUMN IF NOT EXISTS reported_configuration_at TIMESTAMPTZ NULL;

COMMIT;
