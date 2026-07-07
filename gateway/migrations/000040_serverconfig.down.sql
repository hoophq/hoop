BEGIN;

SET search_path TO private;

DROP TABLE IF EXISTS serverconfig;
DROP TYPE IF EXISTS enum_generic_toggle_status;

COMMIT;
