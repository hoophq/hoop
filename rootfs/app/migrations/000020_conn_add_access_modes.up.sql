BEGIN;

SET search_path TO private;

CREATE TYPE enum_access_status AS ENUM ('enabled', 'disabled');

ALTER TABLE connections ADD COLUMN access_mode_runbooks enum_access_status;
ALTER TABLE connections ADD COLUMN access_mode_exec enum_access_status;
ALTER TABLE connections ADD COLUMN access_mode_connect enum_access_status;
ALTER TABLE connections ADD COLUMN access_schema enum_access_status;

-- enable old connections with defaults (enabled)
UPDATE connections
    SET access_mode_runbooks = 'enabled',
        access_mode_exec = 'enabled',
        access_mode_connect = 'enabled',
        access_schema = 'enabled';

COMMIT;
