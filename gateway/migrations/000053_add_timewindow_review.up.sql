BEGIN;

SET search_path TO private;

ALTER TABLE reviews ADD time_window jsonb NULL;

COMMIT;
