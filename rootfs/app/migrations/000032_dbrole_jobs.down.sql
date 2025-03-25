BEGIN;

DROP TABLE private.dbrole_jobs;
ALTER TABLE private.env_vars DROP COLUMN updated_at;

COMMIT;
