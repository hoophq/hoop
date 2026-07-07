BEGIN;

SET search_path TO private;

ALTER TABLE connections ALTER COLUMN force_approve_groups SET NOT NULL;

COMMIT;
