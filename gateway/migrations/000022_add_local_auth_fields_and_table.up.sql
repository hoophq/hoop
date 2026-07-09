BEGIN;

SET search_path TO private;

ALTER TABLE users ADD COLUMN hashed_password TEXT;

COMMIT;
