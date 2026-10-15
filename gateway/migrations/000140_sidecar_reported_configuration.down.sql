BEGIN;

SET search_path TO private;

ALTER TABLE sidecars
    DROP COLUMN IF EXISTS reported_configuration,
    DROP COLUMN IF EXISTS reported_configuration_at;

COMMIT;
