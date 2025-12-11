BEGIN;

SET search_path TO private;

ALTER TABLE reviews DROP COLUMN time_window;

COMMIT;
