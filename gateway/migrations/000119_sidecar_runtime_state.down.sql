BEGIN;

SET search_path TO private;

ALTER TABLE sidecars
    DROP COLUMN IF EXISTS last_seen_at,
    DROP COLUMN IF EXISTS reported_version,
    DROP COLUMN IF EXISTS served_revision,
    DROP COLUMN IF EXISTS applied_revision,
    DROP COLUMN IF EXISTS last_outcome;

COMMIT;
