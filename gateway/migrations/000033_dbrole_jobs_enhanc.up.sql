BEGIN;

SET search_path TO private;

ALTER TABLE private.dbrole_jobs DROP COLUMN status;
ALTER TABLE private.dbrole_jobs DROP COLUMN error_message;
ALTER TABLE private.dbrole_jobs DROP COLUMN updated_at;
ALTER TABLE private.dbrole_jobs ADD COLUMN status JSON NULL;
ALTER TABLE private.dbrole_jobs ADD COLUMN completed_at TIMESTAMP NULL;

COMMIT;
