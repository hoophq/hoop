BEGIN;

SET search_path TO private;

ALTER TABLE private.sessions ADD COLUMN exit_code SMALLINT NULL;
ALTER TABLE private.sessions ADD COLUMN connection_subtype VARCHAR(64) NULL;

COMMIT;
