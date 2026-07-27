BEGIN;

SET search_path TO private;

ALTER TABLE orgs ADD COLUMN license_data JSON NULL;

COMMIT;