BEGIN;

SET search_path TO private;

ALTER TABLE connections 
DROP COLUMN access_mode_runbooks,
DROP COLUMN access_mode_exec,
DROP COLUMN access_mode_connect,
DROP COLUMN access_schema;

DROP TYPE IF EXISTS enum_access_status;

COMMIT;
