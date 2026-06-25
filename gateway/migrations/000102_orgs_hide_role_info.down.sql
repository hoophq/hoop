BEGIN;
SET search_path TO private;

ALTER TABLE orgs DROP COLUMN IF EXISTS hide_role_info;

COMMIT;
