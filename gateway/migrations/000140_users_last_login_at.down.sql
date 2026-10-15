BEGIN;

SET search_path TO private;

ALTER TABLE users DROP COLUMN IF EXISTS last_login_at;

COMMIT;
