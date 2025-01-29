BEGIN;

SET search_path TO private;

ALTER TABLE private.sessions DROP COLUMN exit_code;
ALTER TABLE private.sessions DROP COLUMN connection_subtype;

COMMIT;