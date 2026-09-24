BEGIN;

SET search_path TO private;

-- The config keys and protocols a sidecar reported it accepts. A write that
-- uses anything else is refused, because the sidecar would refuse the whole
-- document. NULL is a sidecar that never reported, which is not gated.
ALTER TABLE sidecars
    ADD COLUMN IF NOT EXISTS reported_config_keys TEXT[] NULL,
    ADD COLUMN IF NOT EXISTS reported_protocols   TEXT[] NULL;

COMMIT;
