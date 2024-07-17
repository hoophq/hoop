BEGIN;

SET search_path TO private;

CREATE TYPE enum_access_status AS ENUM ('enabled', 'disabled');

ALTER TABLE connections ADD COLUMN access_mode_runbooks enum_access_status NOT NULL;
ALTER TABLE connections ADD COLUMN access_mode_exec enum_access_status NOT NULL;
ALTER TABLE connections ADD COLUMN access_mode_connect enum_access_status NOT NULL;
ALTER TABLE connections ADD COLUMN access_schema enum_access_status NOT NULL;

UPDATE connections SET access_mode_runbooks = 'enabled' WHERE access_mode_runbooks IS NULL;
UPDATE connections SET access_mode_exec = 'enabled' WHERE access_mode_exec IS NULL;
UPDATE connections SET access_mode_connect = 'enabled' WHERE access_mode_connect IS NULL;
UPDATE connections SET access_schema = 'enabled' WHERE access_schema IS NULL;

COMMIT;
