BEGIN;

SET search_path TO private;

ALTER TABLE sidecars
    DROP COLUMN IF EXISTS reported_config_keys,
    DROP COLUMN IF EXISTS reported_protocols;

COMMIT;
