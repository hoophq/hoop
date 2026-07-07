BEGIN;

SET search_path TO private;

ALTER TABLE private.orgs DROP COLUMN license_data;

COMMIT;