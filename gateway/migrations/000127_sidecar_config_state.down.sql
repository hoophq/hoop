BEGIN;

SET search_path TO private;

ALTER TABLE sidecars
    DROP COLUMN IF EXISTS last_error,
    DROP COLUMN IF EXISTS served_revision_at;

COMMIT;
